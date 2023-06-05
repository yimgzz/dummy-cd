package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/yimgzz/dummy-cd/server/pkg/instance"
	"github.com/yimgzz/dummy-cd/server/pkg/pb"
	dummycd "github.com/yimgzz/dummy-cd/server/pkg/server"
	"github.com/yimgzz/dummy-cd/server/pkg/util"
	"google.golang.org/grpc"
	"net"
	"os"
	"path"
	"runtime"
	"strconv"
	"sync"
)

var (
	port     = flag.Int("port", 50031, "grpc server port")
	logLevel = flag.String("log-level", "info", "info, error, debug or trace")
)

func main() {
	flag.Parse()

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
		CallerPrettyfier: func(frame *runtime.Frame) (function string, file string) {
			name := path.Base(frame.File) + ":" + strconv.Itoa(frame.Line)
			return frame.Function, name
		},
	})

	log.SetReportCaller(true)

	switch *logLevel {
	case "info":
		log.SetLevel(log.InfoLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "trace":
		log.SetLevel(log.TraceLevel)
	}

	kubeConfig, err := instance.NewKubernetesConfig()

	util.PanicOnError(err)

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))

	util.PanicOnError(err)

	namespaceFile := "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

	currentNamespace := util.ReadFile(&namespaceFile)

	if currentNamespace == nil {
		util.PanicOnError(errors.New("error while define current namespace"))
	}

	server := grpc.NewServer()

	ctx := context.Background()

	appServer := &dummycd.Server{
		Handler: &instance.RepositoryHandler{
			Mutex:            new(sync.Mutex),
			CurrentNamespace: string(*currentNamespace),
			Ctx:              ctx,
		},
		KubeConfig: kubeConfig,
		Ctx:        ctx,
	}

	pb.RegisterDummycdServer(server, appServer)

	log.Debugf("gRPC server running on :%d", *port)

	go func() {
		if err := server.Serve(listener); err != nil {
			log.Fatal(err)
			os.Exit(-1)
		}
	}()

	done := make(chan bool)
	go appServer.Handler.Handle(kubeConfig, &done)
	<-done
}
