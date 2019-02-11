package main

/*
 * cabal.go
 * Implements the gRPC server and client for the gmbhCore Cabal Server
 * Abe Dick
 * Nov 2018
 */

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gmbh-micro/cabal"
	"github.com/gmbh-micro/defaults"
	"github.com/gmbh-micro/notify"
	"github.com/gmbh-micro/service"
	"github.com/gmbh-micro/service/process"
)

// cabalServer is for gRPC interface for the gmbhCore service coms server
type cabalServer struct{}

func (s *cabalServer) EphemeralRegisterService(ctx context.Context, in *cabal.RegServReq) (*cabal.RegServRep, error) {

	lookupService, err := core.Router.LookupService(in.NewServ.GetName())
	if err != nil {
		if err.Error() == "router.LookupService.nameNotFound" {
			if in.NewServ.GetMode() != cabal.NewService_MANAGED {
				lookupService, err = core.registerPlanetaryService(
					in.GetNewServ().GetName(),
					in.GetNewServ().GetAliases(),
					in.GetNewServ().GetIsClient(),
					in.GetNewServ().GetIsServer())
				if err != nil {
					return &cabal.RegServRep{Status: err.Error()}, nil
				}
			}
		}
	}

	if !core.Config.Daemon {
		notify.StdMsgMagentaNoPrompt(fmt.Sprintf("[serv] <(%s)- processing ephem-reg request; name=(%s); aliases=(%s); mode=(%s)", lookupService.ID, in.NewServ.GetName(), strings.Join(in.NewServ.GetAliases(), ","), lookupService.GetMode()))
		if lookupService.Static.IsServer {
			notify.StdMsgMagentaNoPrompt(fmt.Sprintf("       -(%s)> success; address=(%v)", lookupService.ID, lookupService.Address))
		} else {
			notify.StdMsgMagentaNoPrompt(fmt.Sprintf("       -(%s)> success;", lookupService.ID))
		}
	}

	reply := &cabal.RegServRep{
		Status:   "acknowledged",
		ID:       lookupService.ID,
		CorePath: core.ProjectPath,
		Address:  lookupService.Address,
	}

	return reply, nil
}

func (s *cabalServer) MakeDataRequest(ctx context.Context, in *cabal.DataReq) (*cabal.DataResp, error) {

	reqHandler := newRequestHandler(in.GetReq())
	reqHandler.Fulfill()

	return &cabal.DataResp{Resp: reqHandler.GetResponder()}, nil
}

func (s *cabalServer) UnregisterService(ctx context.Context, in *cabal.UnregisterReq) (*cabal.UnregisterResp, error) {
	reply := &cabal.UnregisterResp{Awk: false}
	return reply, nil
}

func (s *cabalServer) QueryStatus(ctx context.Context, in *cabal.QueryRequest) (*cabal.QueryResponse, error) {
	return nil, nil
}

func (s *cabalServer) UpdateServiceRegistration(ctx context.Context, in *cabal.ServiceUpdate) (*cabal.ServiceUpdate, error) {
	return &cabal.ServiceUpdate{Message: "unimp"}, nil
}

//////////////////////////////////////////////////////////////////////////////////////////
// Converters
//////////////////////////////////////////////////////////////////////////////////////////

// ServiceToRPC translates one service to cabal form
func ServiceToRPC(s service.Service) *cabal.Service {

	procRuntime := s.GetProcess().GetRuntime()

	rpcService := &cabal.Service{
		Id:      s.ID,
		Name:    s.Static.Name,
		Path:    s.Path,
		LogPath: s.Path + defaults.SERVICE_LOG_PATH + defaults.SERVICE_LOG_FILE,
	}

	if s.Mode == service.Managed {

		rpcService.Pid = int32(procRuntime.Pid)
		rpcService.Fails = int32(procRuntime.Fails)
		rpcService.Address = s.Address
		rpcService.Restarts = int32(procRuntime.Restarts)
		rpcService.StartTime = procRuntime.StartTime.Format(time.RFC3339)
		rpcService.FailTime = procRuntime.DeathTime.Format(time.RFC3339)
		rpcService.Errors = s.GetProcess().ReportErrors()

		rpcService.Mode = "managed"
		switch s.Process.GetStatus() {
		case process.Stable:
			rpcService.Status = "Stable"
		case process.Running:
			rpcService.Status = "Running"
		case process.Degraded:
			rpcService.Status = "Degraded"
		case process.Failed:
			rpcService.Status = "Failed"
		case process.Killed:
			rpcService.Status = "Killed"
		case process.Initialized:
			rpcService.Status = "Initialized"
		}
	} else if s.Mode == service.Remote {
		rpcService.Mode = "remote"
		rpcService.Status = "-"
	}
	return rpcService
}

func serviceToStruct() *service.Service {
	return nil
}
