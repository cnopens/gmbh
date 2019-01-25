package core

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/gimlet-gmbh/gimlet/cabal"
	grouter "github.com/gimlet-gmbh/gimlet/grouter"
	notify "github.com/gimlet-gmbh/gimlet/notify"
	pmgmt "github.com/gimlet-gmbh/gimlet/pmgmt"
	service "github.com/gimlet-gmbh/gimlet/service"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	yaml "gopkg.in/yaml.v2"
)

/**
TODO: gproto to cabal
*/

/**
 * gcore.go
 * Abe Dick
 * January 2019
 */

// The global config and controller for the core
// Not much of a way around this when using rpc
var core *Core

const (
	addr = "localhost:59999"
	sep  = "------------------------------------------------------------------------"
)

// Core - internal representation of the gimlet core
type Core struct {
	Version        string
	CodeName       string
	ProjectConf    *ProjectConfig
	serviceHandler *service.ServiceHandler
	router         *grouter.Router
	log            *os.File

	// ServiceDir is the directory in which services live within a Gimlet project
	ServiceDir string `yaml:"serviceDirectory"`

	// CabalAddress is the address of the RPC server that sends data requests
	CabalAddress string `yaml:"serviceAddress"`

	// CtrlAddress is the address of the RPC server used by ctrl to manage processes
	CtrlAddress string `yaml:"ctrlAddress"`

	// ProjectPath is the path to the root folder of the gimlet project
	ProjectPath string
}

// ProjectConfig is the configuration of the gimlet project located in the main directory
type ProjectConfig struct {
	Name             string `yaml:"Name"`
	ServiceDirectory string `yaml:"ServiceDirectory"`
}

// tmpService is used during service discovery to hold tmp dat
type tmpService struct {
	path       string
	configFile string
}

// StartCore initializes settings of the core and creates the service handler and router
// needed to process requestes
func StartCore(path string) *Core {
	core = &Core{
		Version:     "00.07.01",
		CodeName:    "Control",
		ServiceDir:  "services",
		ProjectPath: path,
		serviceHandler: &service.ServiceHandler{
			Services: make(map[string]*service.ServiceControl),
			Names:    make([]string, 0),
		},
		router: &grouter.Router{
			BaseAddress:    "localhost:",
			NextPortNumber: 40010,
			Localhost:      true,
		},
	}
	core.ProjectConf = core.parseProjectYamlConfig(path + "/gimlet.yaml")
	core.logRuntimeData(path + "/gimlet/")
	return core
}

// StartInternalServer starts the gRPC server to run core on
func (c *Core) StartInternalServer() {
	notify.StdMsgBlue("Attempting to start internal server")
	c.rpcConnect()
}

// ServiceDiscovery scans all directories in the ./services folder looking for gimlet configuration files
func (c *Core) ServiceDiscovery() {
	path := c.getServicePath()

	notify.StdMsgBlue("service discovery started in")
	notify.StdMsgBlue(path)

	tmpService := c.findAllServices(path)
	for i, nservice := range tmpService {

		staticData := c.parseYamlConfig(nservice.path + nservice.configFile)

		notify.StdMsgBlue(fmt.Sprintf("(%d/%d)", i+1, len(tmpService)))
		notify.StdMsgBlue(staticData.Name, 1)
		notify.StdMsgBlue(path+"/"+staticData.Path, 1)

		newService := service.NewServiceControl(staticData)
		err := c.serviceHandler.AddService(newService)
		if err != nil {
			notify.StdMsgErr("Could not add service")
			notify.StdMsgErr("reported error: " + err.Error())
			continue
		}

		if staticData.IsServer {
			newService.Address = c.router.GetNextAddress()
			notify.StdMsgBlue("Assigning address: "+newService.Address, 1)
		}

		newService.BinPath = nservice.path + newService.Static.Path
		newService.ConfigPath = nservice.path

		pid, err := c.startService(newService)
		if err != nil {
			notify.StdMsgErr("Could not add service", 1)
			notify.StdMsgErr("reported error: "+err.Error(), 1)
			continue
		}

		notify.StdMsgGreen(fmt.Sprintf("Service running in ephemeral mode with pid=(%v)", pid), 1)
	}

	go c.takeInventory()

	notify.StdMsgBlue("Startup complete")
	notify.StdMsgGreen("Blocking main thread until SIGINT")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT)
	_ = <-sig

	notify.StdMsgMagenta("Recieved shutdown signal")
	c.shutdown()

	os.Exit(0)
}

func (c *Core) getServicePath() string {
	return c.ProjectPath + "/" + c.ProjectConf.ServiceDirectory
}

func (c *Core) startService(service *service.ServiceControl) (string, error) {

	if service.Static.Language == "go" {
		// fmt.Println("service config path: " + service.ConfigPath)
		service.Process = pmgmt.NewGoProcess(service.Name, service.BinPath, service.ConfigPath)
		pid, err := service.Process.Controller.Start(service.Process)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(pid), nil
	}

	return "", nil
}

// findAllServices looks for .yaml files in subdirectories of baseDir
// baseDir/dir/*.yaml
// TODO: Need to verify that we are getting the correct yaml file
// if there are several yaml files and if there are no yaml
// ALL ERRORS NEED TO BE HANDLED BETTER THAN WITH LOG.FATAL()
func (c *Core) findAllServices(baseDir string) []tmpService {
	services := []tmpService{}

	baseDirFiles, err := ioutil.ReadDir(baseDir)
	if err != nil {
		log.Fatal(err)
	}

	// For every file in the baseDirectory
	for _, file := range baseDirFiles {

		// eval symbolicLinks first
		fpath := baseDir + "/" + file.Name()
		potentialSymbolic, err := filepath.EvalSymlinks(fpath)
		if err != nil {
			notify.StdMsgErr(err.Error(), 0)
			continue
		}

		// If it wasn't a symbolic path check if it was a dir, skip if not
		if fpath == potentialSymbolic {
			if !file.IsDir() {
				continue
			}
		}

		// Try and open the symbolic link path and check for dir, skip if not
		newFile, err := os.Stat(potentialSymbolic)
		if err != nil {
			notify.StdMsgErr(err.Error())
			continue
		}

		if !newFile.IsDir() {
			continue
		}

		// Looking through potential gmbH service directory
		serviceFiles, err := ioutil.ReadDir(baseDir + "/" + file.Name())
		if err != nil {
			log.Fatal(err)
		}

		for _, sfile := range serviceFiles {
			match, err := regexp.MatchString(".yaml", sfile.Name())
			if err == nil && match {
				newService := tmpService{
					path:       baseDir + "/" + file.Name() + "/",
					configFile: sfile.Name(),
				}
				services = append(services, newService)
			}
		}
	}

	return services
}

func (c *Core) parseYamlConfig(path string) *service.StaticControl {
	var static service.StaticControl
	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		log.Printf("yamlFile.Get err   #%v ", err)
	}
	err = yaml.Unmarshal(yamlFile, &static)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}
	return &static
}

func (c *Core) parseProjectYamlConfig(path string) *ProjectConfig {
	var conf ProjectConfig
	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		log.Printf("yamlFile.Get err   #%v ", err)
	}
	err = yaml.Unmarshal(yamlFile, &conf)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}
	return &conf
}

// TODO:
// this will orphan the gothread
// still need to do signal hadnling
// see the coms package for how it was done there
func (c *Core) rpcConnect() {

	notify.StdMsgGreen("Starting gmbH Core Server at: "+addr, 1)

	go func() {
		list, err := net.Listen("tcp", addr)
		if err != nil {
			panic(err)
		}

		s := grpc.NewServer()
		cabal.RegisterCabalServer(s, &_server{})

		reflection.Register(s)
		if err := s.Serve(list); err != nil {
			panic(err)
		}

	}()

}

func (c *Core) logRuntimeData(path string) {
	filename := ".gimlet"
	var err error
	c.log, err = notify.OpenLogFile(path, filename)
	if err != nil {
		notify.StdMsgErr("could not create log file: " + err.Error())
		return
	}

	c.log.WriteString("\n" + sep + "\n")
	c.log.WriteString("startTime=\"" + time.Now().Format("Jan 2 2006 15:04:05 MST") + "\"\n")
	c.log.WriteString("cabalAddress=\"" + addr + "\"\n")
	c.log.WriteString("ctrlAddress=\"" + "-" + "\"\n")

}

func (c *Core) takeInventory() {
	serviceString := "services=["
	for _, serviceName := range c.serviceHandler.Names {
		service := c.serviceHandler.Services[serviceName]
		serviceString += "\"" + service.Name + "-" + service.ConfigPath + "\", "
	}
	serviceString = serviceString[:len(serviceString)-2]
	serviceString += "]"
	c.log.WriteString(serviceString + "\n")
}

func (c *Core) shutdown() {
	c.serviceHandler.KillAllServices()
	c.log.WriteString("stopTime=\"" + time.Now().Format("Jan 2 2006 15:04:05 MST") + "\"\n")
	c.log.Close()
}