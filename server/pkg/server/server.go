package server

import (
	"context"
	log "github.com/sirupsen/logrus"
	"github.com/yimgzz/dummy-cd/server/pkg/instance"
	"github.com/yimgzz/dummy-cd/server/pkg/pb"
	"github.com/yimgzz/dummy-cd/server/pkg/provider"
	"github.com/yimgzz/dummy-cd/server/pkg/util"
	"k8s.io/client-go/rest"
)

type Server struct {
	pb.UnimplementedDummycdServer
	KubeConfig *rest.Config
	Handler    *instance.RepositoryHandler
	Ctx        context.Context
}

func (s *Server) AddRepository(ctx context.Context, in *pb.Repository) (*pb.Empty, error) {
	log.Debugf("request recieved: %+v", in)

	repositoryConfig := s.Handler.GetRepositoryConfig(in.GetUrl())

	if repositoryConfig != nil {
		log.Debugf("config for url already exist %s: %s", in.GetName(), in.GetUrl())
		return &pb.Empty{}, nil
	}

	for !s.Handler.Mutex.TryLock() {
	}

	defer s.Handler.Mutex.Unlock()

	repositoryConfig, err := instance.NewRepositoryConfig(in.GetName(),
		&instance.RepositorySettings{
			URL:                   in.GetUrl(),
			PrivateKeySecret:      in.GetPrivateKeySecret(),
			InsecureIgnoreHostKey: in.GetInsecureIgnoreHostKey(),
		}, s.KubeConfig, &s.Handler.CurrentNamespace)

	if err != nil {
		log.Error(err)
		return &pb.Empty{}, err
	}

	err = s.Handler.AddRepository(repositoryConfig)

	if err != nil {
		log.Error(err)
		return &pb.Empty{}, err
	}

	log.Infof("repository added: %s", repositoryConfig.Settings.URL)

	return &pb.Empty{}, nil
}

func (s *Server) DeleteRepository(ctx context.Context, in *pb.Repository) (*pb.Empty, error) {
	log.Debugf("request recieved: %+v", in)

	for !s.Handler.Mutex.TryLock() {
	}

	defer s.Handler.Mutex.Unlock()

	err := s.Handler.DeleteRepository(in.GetName())

	if err != nil {
		log.Error(err)
	}

	return &pb.Empty{}, err
}

func (s *Server) AddOrUpdateApplication(ctx context.Context, in *pb.Application) (*pb.Empty, error) {
	log.Debugf("request recieved: %+v", in)

	repository := s.Handler.GetRepositoryConfig(in.GetUrl())

	if repository == nil {
		log.Errorf("%s:repository not found for application", in.GetName())
		return &pb.Empty{}, util.ErrRepositoryConfigNotFound
	}

	err := repository.AddOrUpdateApplication(s.Ctx, s.KubeConfig, &instance.Application{
		RepositoryConfig: repository,
		Name:             in.GetName(),
		Namespace:        in.GetNamespace(),
		Reference:        in.GetReference(),
		URL:              in.GetUrl(),
		SparsePath:       in.GetSparsePath(),
		Helm: &provider.HelmProvider{
			ValueFiles: in.GetHelm().GetValuesFiles(),
			ActionOptions: &provider.HelmActionOptions{
				CheckValuesEqual: in.GetHelm().GetCheckValuesEqual(),
				ReInstallRelease: in.GetHelm().GetReInstallRelease(),
				CreateNamespace:  in.GetHelm().GetCreateNamespace(),
				Atomic:           in.GetHelm().GetAtomic(),
				IncludeCRDs:      in.GetHelm().GetIncludeCRDs(),
			},
		},
	})

	if err != nil {
		if err == util.NoErrAlreadyUpTodate {
			log.Debugf("application already up to date %s: %s", in.GetName(), in.GetUrl())
			return &pb.Empty{}, nil
		}

		log.Errorf("%s: %s", in.GetName(), err)
		return &pb.Empty{}, err
	}

	log.Infof("application added: %s", in.GetName())

	return &pb.Empty{}, nil
}

func (s *Server) DeleteApplication(ctx context.Context, in *pb.Application) (*pb.Empty, error) {
	log.Debugf("request recieved: %+v", in)

	var err error

	for _, repo := range s.Handler.Repos {
		if repo.Settings.URL != in.GetUrl() {
			continue
		}

		err = repo.DeleteApplication(in.GetName())

		if err != nil {
			log.Error(err)
		}

		break
	}

	return &pb.Empty{}, err
}

func (s *Server) GetApplications(ctx context.Context, in *pb.Empty) (*pb.Applications, error) {
	log.Debugf("request recieved: %+v", in)

	var applications []*pb.Application

	for _, r := range s.Handler.Repos {
		for _, a := range r.Apps {
			applications = append(applications, &pb.Application{
				Name:      a.Name,
				Namespace: a.Namespace,
				Url:       a.URL,
				Reference: a.Reference,
				Revision:  &pb.Revision{Hash: a.CurrentRevision.String()},
			})
		}
	}

	return &pb.Applications{Items: applications}, nil
}

func (s *Server) GetApplicationRevisions(ctx context.Context, in *pb.Application) (*pb.Revisions, error) {
	log.Debugf("request recieved: %+v", in)

	app := s.Handler.GetApplication(in.GetName(), in.GetUrl())

	if app == nil {
		return &pb.Revisions{}, util.ErrApplicationNotFound
	}

	appRevisions, err := app.GetRevisionStringsMap()

	if err != nil {
		return &pb.Revisions{}, err
	}

	var revisions []*pb.Revision
	for _, s := range appRevisions {
		revisions = append(revisions, &pb.Revision{Hash: s["hash"], Message: s["message"]})
	}

	return &pb.Revisions{Items: revisions}, nil
}

func (s *Server) CheckoutApplicationRevision(ctx context.Context, in *pb.Application) (*pb.Empty, error) {
	//app := s.handler.GetApplication(in.GetName(), in.GetUrl())
	//
	//if app == nil {
	//	return &pb.Empty{}, ErrApplicationNotFound
	//}
	//
	//err := app.checkoutRevision(plumbing.NewHash(in.GetRevision().GetHash()))

	return &pb.Empty{}, nil
}
