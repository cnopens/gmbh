package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gmbh-micro/cabal"
	"github.com/gmbh-micro/defaults"
	"github.com/gmbh-micro/notify"
	"github.com/gmbh-micro/rpc"
	yaml "gopkg.in/yaml.v2"
)

// internal reference to core for use rpc
var core *Core

// Core is the main gmbh controller
type Core struct {
	Version string
	Code    string

	// the filesystem directory to the gmbh project where assumptions can be made about
	// structure accoring to the config file
	ProjectPath string

	// con holds the host connection for the cabal server
	con *rpc.Connection

	// config is the user configurable parameters as read in from file
	config *UserConfig

	// Router controls all aspects of data requests & handling in Core
	Router *Router

	msgCounter  int
	startTime   time.Time
	log         *notify.Log
	mu          *sync.Mutex
	verbose     bool
	verboseData bool
}

// NewCore initializes settings of the core and instantiates the core struct which includes the
// service router and handlers
func NewCore(cPath string, verbose, verboseData bool) (*Core, error) {

	// cannot reinit core once it has been created
	if core != nil {
		return core, nil
	}

	userConfig, err := ParseUserConfig(cPath)
	if err != nil {
		notify.LnRedF("could not parse config; err=%v", err.Error())
		return nil, err
	}

	core = &Core{
		Version:     defaults.VERSION,
		Code:        defaults.CODE,
		ProjectPath: basePath(cPath),
		con:         rpc.NewCabalConnection(defaults.DEFAULT_HOST+defaults.DEFAULT_PORT, &cabalServer{}),
		config:      userConfig,
		Router:      NewRouter(),
		msgCounter:  1,
		startTime:   time.Now(),
		mu:          &sync.Mutex{},
		verbose:     verbose,
	}

	if core.ProjectPath == "" {
		notify.LnRedF("could not calculate path to project")
		return nil, errors.New("config path error")
	}

	// notify.LnCyanF("                    _           ")
	// notify.LnCyanF("  _  ._ _  |_  |_| /   _  ._ _  ")
	// notify.LnCyanF(" (_| | | | |_) | | \\_ (_) | (/_")
	// notify.LnCyanF("  _|                            ")
	notify.LnCyanF("                    _            _              ")
	notify.LnCyanF("  _  ._ _  |_  |_| /   _  ._ _  | \\  _. _|_  _. ")
	notify.LnCyanF(" (_| | | | |_) | | \\_ (_) | (/_ |_/ (_|  |_ (_| ")
	notify.LnCyanF("  _|                                            ")
	notify.LnCyanF("version=%v; code=%v; startTime=%s", core.Version, core.Code, core.startTime.Format(time.Stamp))

	return core, nil
}

// GetCore retrieves the instance of core. For use with rpc server
func GetCore() (*Core, error) {
	if core != nil {
		return core, nil
	}
	return nil, errors.New("core.GetCore.internalError")
}

// Start the cabal server
func (c *Core) Start() {
	err := c.con.Connect()
	if err != nil {
		c.ve("could not connected; err=%s", err.Error())
		return
	}
	c.v("connected; address=%s", c.con.Address)

	// c.serviceDiscovery()

	c.Wait()
}

// Wait holds the main program thread until shutdown signal is received
func (c *Core) Wait() {
	sig := make(chan os.Signal, 1)

	if os.Getenv("PMMODE") == "PMManaged" {
		c.vi("overriding sigusr2")
		signal.Notify(sig, syscall.SIGUSR2)
		signal.Ignore(syscall.SIGUSR1, syscall.SIGINT)
	} else {
		c.vi("overriding sigint")
		signal.Notify(sig, syscall.SIGINT)
	}

	c.v("main thread waiting")
	_ = <-sig
	fmt.Println() //dead line to line up output

	c.shutdown(false)
}

// shutdown begins graceful shutdown procedures
func (c *Core) shutdown(remote bool) {
	c.v("shutdown procedure started")

	// send shutdown notification to all services
	c.Router.sendShutdownNotices()

	time.Sleep(time.Second * 5)
	os.Exit(0)
}

// v verbose helper
func (c *Core) v(format string, a ...interface{}) {
	notify.LnCyanF("[core] "+format, a...)
}

// ve verbose helper
func (c *Core) ve(format string, a ...interface{}) {
	notify.LnRedF("[core] "+format, a...)
}

// vi verbose helper
func (c *Core) vi(format string, a ...interface{}) {
	notify.LnYellowF("[core] "+format, a...)
}

// basePath attempts to get the absolute path to the directory in which the config file is specified
func basePath(configPath string) string {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		notify.LnRedF("error=%v", err.Error())
		return ""
	}
	return filepath.Dir(abs)
}

/**********************************************************************************
**** User Config
**********************************************************************************/

// UserConfig represents the parsable config settings
type UserConfig struct {
	Name              string   `yaml:"project_name"`
	Verbose           bool     `yaml:"verbose"`
	Daemon            bool     `yaml:"daemon"`
	DefaultHost       string   `yaml:"default_host"`
	DefaultPort       string   `yaml:"default_port"`
	ControlHost       string   `yaml:"control_host"`
	ControlPort       string   `yaml:"control_port"`
	ServicesDirectory string   `yaml:"services_directory"`
	ServicesToAttach  []string `yaml:"services_to_attach"`
	ServicesDetached  []string `yaml:"services_detached"`
}

// ParseUserConfig attempts to parse a yaml file at path and return the UserConfigStruct.
// If not all settings have been defined in user path, the defaults will be used.
func ParseUserConfig(path string) (*UserConfig, error) {
	c := UserConfig{Verbose: defaults.VERBOSE, Daemon: defaults.DAEMON}

	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, errors.New("could not open yaml file: " + err.Error())
	}

	err = yaml.Unmarshal(yamlFile, &c)
	if err != nil {
		return nil, errors.New("could not parse yaml file: " + err.Error())
	}

	if c.Name == "" {
		c.Name = defaults.PROJECT_NAME
	}
	if c.DefaultHost == "" {
		c.DefaultHost = defaults.DEFAULT_HOST
	}
	if c.DefaultPort == "" {
		c.DefaultPort = defaults.DEFAULT_PORT
	}
	if c.ControlHost == "" {
		c.ControlHost = defaults.CONTROL_HOST
	}
	if c.ControlPort == "" {
		c.ControlPort = defaults.CONTROL_PORT
	}
	return &c, nil
}

/**********************************************************************************
**** Router
**********************************************************************************/

// Router handles all of the addressing and mapping of services that are attached to gmbhCore
type Router struct {

	// services (Name|Alias)->Service
	// map contains all registered services
	services map[string]*GmbhService

	// serviceNames is a list of the names of all services attached. This is useful because if the
	// map is walked using a range it will return a value for every alias and thus have duplicates
	serviceNames []string

	// idCounter keeps track of the current runnig id
	idCounter int

	// addressHandler is in charge of assigning addresses and making sure that there are no collisions
	addressing *addressHandler

	verbose bool
	mu      *sync.Mutex
}

// NewRouter instantiates and returns a new Router structure
func NewRouter() *Router {

	r := &Router{
		services:     make(map[string]*GmbhService),
		serviceNames: make([]string, 0),
		idCounter:    100,
		addressing: &addressHandler{
			host: defaults.LOCALHOST,
			port: defaults.CORE_START,
			mu:   &sync.Mutex{},
		},
		mu:      &sync.Mutex{},
		verbose: true,
	}

	go r.pingHandler()

	return r
}

// LookupService looks through the services map and returns the service if it exists
func (r *Router) LookupService(name string) (*GmbhService, error) {
	r.v("looking up %s", name)
	retrievedService := r.services[name]
	if retrievedService == nil {
		r.v("not found")
		return nil, errors.New("router.LookupService.NotFound")
	}
	r.v("found")
	return retrievedService, nil
}

// AddService attaches a service to gmbH
func (r *Router) AddService(name string, aliases []string) (*GmbhService, error) {

	newService := NewService(
		r.assignNextID(),
		name,
		aliases,
		r.addressing.assignAddress(),
	)

	// check to see if it exists in map already
	s, err := r.LookupService(name)
	if err == nil {
		r.v("found new service already in map")
		if s.State == Shutdown {
			r.v("state is reported as shutdown")
			r.v("acting as if this is the same service")
			s.UpdateState(Running)
			return s, nil
		}
		r.v("state was not reported as shutdown, probable err")
	}

	err = r.addToMap(newService)
	if err != nil {
		r.v(newService.String())
		r.v("could not add service to map; err=%s", err.Error())
		return nil, err
	}

	r.v("added service=%s", newService.String())
	return newService, nil
}

// Verify a ping
func (r *Router) Verify(name, id, address string) error {
	s := r.services[name]
	if s == nil {
		return errors.New("verify.notFound")
	}
	if s.ID != id {
		return errors.New("verify.badID")
	}
	if s.Address != address {
		return errors.New("verify.badAddress")
	}
	if s.State == Shutdown {
		return errors.New("verify.reportedShutdown")
	}
	if s.State == Unresponsive {
		s.UpdateState(Running)
	}
	s.LastPing = time.Now()
	return nil
}

// addToMap returns an error if there is a name or alias conflict with an existing
// service in the service map, otherwise the service's name and alias are added to
// the map
func (r *Router) addToMap(newService *GmbhService) error {

	if _, ok := r.services[newService.Name]; ok {
		r.v("could not add to map, duplicate name")
		return errors.New("router.addToMap: duplicate service with same name found")
	}

	for _, alias := range newService.Aliases {
		if _, ok := r.services[alias]; ok {
			r.v("could not add to map, duplicate alias=" + alias)
			return errors.New("router.addToMap: duplicate service with same alias found")
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.services[newService.Name] = newService
	r.serviceNames = append(r.serviceNames, newService.Name)
	for _, alias := range newService.Aliases {
		r.services[alias] = newService
	}

	r.v("added %s to map", newService.Name)

	return nil
}

// sendShutdownNotices sends a notice to all clients that core is shutting down
func (r *Router) sendShutdownNotices() {
	for _, n := range r.serviceNames {
		service := r.services[n]
		r.v("sending shutdown to %s at %s", service.Name, service.Address)
		client, ctx, can, err := rpc.GetCabalRequest(service.Address, time.Second*1)
		if err != nil {
			r.v("could not create client")
			can()
			continue
		}
		req := &cabal.ServiceUpdate{
			Sender: "gmbh-core",
			Target: service.Name,
			Action: "core.shutdown",
		}
		_, err = client.UpdateServiceRegistration(ctx, req)
		if err != nil {
			r.v("error contacting service; err=%s", err.Error())
		}
	}
}

// pingHandler looks through each of the remotes in the map. if it has been more than n amount of
// time since a remote has sent a ping, it will be pinged. If the ping is not retured after n more
// seconds, the remote will be marked as Failed After n amount of time, failed remotes will
// be removed from the map
func (r *Router) pingHandler() {
	for {
		time.Sleep(time.Second * 180)
		for _, s := range r.serviceNames {
			if time.Since(r.services[s].LastPing) > time.Second*90 {
				r.v("marking name=%s; id=%s as Unresponsive", s, r.services[s].ID)
				r.services[s].UpdateState(Unresponsive)
			}
		}
	}
}

func (r *Router) assignNextID() string {
	mu := &sync.Mutex{}
	mu.Lock()
	defer mu.Unlock()
	r.idCounter++
	return strconv.Itoa(r.idCounter)
}

// v verbose printer
func (r *Router) v(msg string, a ...interface{}) {
	notify.LnGreenF("[rtr] "+msg, a...)
}

// addressHandler is in charge of assigning addresses to services
type addressHandler struct {
	table map[string]string
	host  string
	port  int
	mu    *sync.Mutex
}

func (a *addressHandler) assignAddress() string {
	a.setNextAddress()
	addr := a.host + ":" + strconv.Itoa(a.port)
	return addr
}

func (a *addressHandler) setNextAddress() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.port += 2
}

/**********************************************************************************
**** Service
**********************************************************************************/

// GmbhService is the data representation of a connected service
type GmbhService struct {
	// The id assigned by the router
	ID string

	// Aliases of the service
	Aliases []string

	// the name of the service
	Name string

	// the address to the service
	Address string

	// The time that the service was added to the router
	Added time.Time

	// The last known state of the service
	State State

	// The last time a ping was received
	LastPing time.Time

	mu *sync.Mutex
}

func (g *GmbhService) String() string {
	return fmt.Sprintf("name=%s; id=%s; address=%s;", g.Name, g.ID, g.Address)
}

// NewService returns a gmbhService object with data filled in
func NewService(id string, name string, aliases []string, address string) *GmbhService {
	return &GmbhService{
		ID:       id,
		Name:     name,
		Aliases:  aliases,
		Address:  address,
		Added:    time.Now(),
		State:    Running,
		LastPing: time.Now().Add(time.Hour),
		mu:       &sync.Mutex{},
	}
}

// UpdateState of the current state of the service
func (g *GmbhService) UpdateState(s State) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.v("marking %s(%s) as %s", g.Name, g.ID, s.String())
	g.State = s
}

// v verbose printer
func (g *GmbhService) v(msg string, a ...interface{}) {
	notify.LnYellowF("[service] "+msg, a...)
}

// State controls the state of a remote server
type State int

const (
	// Running as normal
	Running State = 1 + iota

	// Shutdown notice received from remote
	Shutdown

	// Unresponsive if the service has not sent a ping in greater than some amount of time
	Unresponsive

	// Failed to return a pong
	Failed
)

var states = [...]string{
	"Running",
	"Shutdown",
	"Unresponsive",
	"Failed",
}

func (s State) String() string {
	if Running <= s && s <= Failed {
		return states[s-1]
	}
	return "%!State()"
}

/**********************************************************************************
**** OS Helpers
**********************************************************************************/

// getLogFile attempts to add the desired path as an extension to the current
// directory as reported by os.GetWd(). The file is then opened or created
// and returned
func getLogFile(desiredPathExt, filename string) (*os.File, error) {
	// get pwd
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	// make sure that the path extension exists or make the directories needed
	dirPath := filepath.Join(dir, desiredPathExt)
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		os.Mkdir(dir, 0755)
	}
	// create the file
	filePath := filepath.Join(dirPath, filename)
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func getpwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}
