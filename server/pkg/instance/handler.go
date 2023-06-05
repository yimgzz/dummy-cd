package instance

import (
	"context"
	"flag"
	log "github.com/sirupsen/logrus"
	"github.com/yimgzz/dummy-cd/server/pkg/util"
	"k8s.io/apimachinery/pkg/runtime/schema"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"os"
	"path"
	"path/filepath"
	k8sConfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sync"
	"time"
)

var (
	Workspace           = path.Join(*util.GetUserHome(), ".dummycd", "storage")
	RunLifeCycleTimeout = flag.Int("run-life-cycle-timeout", 60, "timeout to check application changes and run delivery")

	repositoryGVR = schema.GroupVersionResource{
		Group:    "dummy.cd",
		Version:  "v1alpha1",
		Resource: "repositories",
	}

	applicationGVR = schema.GroupVersionResource{
		Group:    "dummy.cd",
		Version:  "v1alpha1",
		Resource: "applications",
	}
)

type RepositoryHandler struct {
	KnownHostsSecret *string
	Repos            []*RepositoryConfig
	Mutex            *sync.Mutex
	CurrentNamespace string
	Ctx              context.Context
}

func (h *RepositoryHandler) Handle(restKubeConfig *rest.Config, done *chan bool) {
	ticker := time.NewTicker(time.Duration(*RunLifeCycleTimeout) * time.Second)
	defer ticker.Stop()

	var wg sync.WaitGroup

	log.Info("server started")

	for {
		select {
		case <-*done:
			return
		case _ = <-ticker.C:
			if h.Mutex.TryLock() {
				log.Debug("check for updates started")

				for _, r := range h.Repos {
					if r.Mutex.TryLock() {
						for _, a := range r.Apps {
							if a.mutex.TryLock() {
								wg.Add(1)
								go func(a *Application, wg *sync.WaitGroup) {
									defer a.mutex.Unlock()
									defer wg.Done()

									err := a.RunLifeCycle()

									if err != nil {
										a.logWithFields().Error(err)
									}
								}(a, &wg)
							} else {
								a.logWithFields().Debug("busy")
								wg.Done()
							}
						}
						r.Mutex.Unlock()
					}
				}
				wg.Wait()
				log.Debug("finished check for updates")
				h.Mutex.Unlock()
			} else {
				log.Debug("busy")
			}
		}
	}
}

func NewKnownHosts(knownHosts string) (*string, error) {
	if len(knownHosts) == 0 {
		return nil, nil
	}

	dir := path.Join(*util.GetUserHome(), "tmp")

	err := os.MkdirAll(dir, os.ModeDir)
	if err != nil {
		return nil, err
	}

	knownHostsFile := path.Join(dir, "known_hosts")
	data := []byte(knownHosts)

	if err := util.WriteFile(&knownHostsFile, &data); err != nil {
		return nil, err
	}

	return &knownHostsFile, nil
}

func NewKubernetesConfig() (*rest.Config, error) {
	cfg := k8sConfig.GetConfigOrDie()

	cfg.Insecure = false
	cfg.QPS = 50
	cfg.Burst = 100

	// when kubeconfig is local file
	if (len(cfg.CertFile) == 0) || (len(cfg.CAFile) == 0) || (len(cfg.KeyFile) == 0) {
		dir := path.Join(Workspace, "serviceaccount")

		if err := os.MkdirAll(dir, 0740); err != nil {
			return nil, err
		}

		if len(cfg.CertFile) == 0 && len(cfg.CertData) > 0 {
			cfg.CertFile = filepath.Join(dir, "cert.crt")

			if err := util.WriteFile(&cfg.CertFile, &cfg.CertData); err != nil {
				return nil, err
			}
		}

		if len(cfg.CAFile) == 0 && len(cfg.CAData) > 0 {
			cfg.CAFile = filepath.Join(dir, "ca.crt")

			if err := util.WriteFile(&cfg.CAFile, &cfg.CAData); err != nil {
				return nil, err
			}
		}

		if len(cfg.KeyFile) == 0 && len(cfg.KeyData) > 0 {
			cfg.KeyFile = filepath.Join(dir, "private.key")

			if err := util.WriteFile(&cfg.KeyFile, &cfg.KeyData); err != nil {
				return nil, err
			}
		}
	}

	return cfg, nil
}

func (h *RepositoryHandler) GetRepositoryConfig(URL string) *RepositoryConfig {

	for _, r := range h.Repos {
		if r.Settings.URL == URL {
			return r
		}
	}

	return nil
}

func (h *RepositoryHandler) AddRepository(newRepository *RepositoryConfig) error {
	for !newRepository.Mutex.TryLock() {
	}

	defer newRepository.Mutex.Unlock()

	for _, repo := range h.Repos {
		if repo.Settings.URL == newRepository.Settings.URL {
			return util.ErrRepositoryAlreadyExist
		}
	}

	h.Repos = append(h.Repos, newRepository)

	return nil
}

func (h *RepositoryHandler) DeleteRepository(name string) error {
	repoIndex := -1
	var repository *RepositoryConfig

	for i, repo := range h.Repos {
		if repo.Name == name {
			repoIndex = i
			repository = repo
		}
	}

	if repoIndex == -1 {
		return util.ErrRepositoryConfigNotFound
	}

	for !repository.Mutex.TryLock() {
	}

	defer repository.Mutex.Unlock()

	var wg sync.WaitGroup

	for _, app := range repository.Apps {
		wg.Add(1)
		go func(app *Application, wg *sync.WaitGroup) {
			defer wg.Done()

			for {
				if app.mutex.TryLock() {
					break
				}
			}

			if err := app.Uninstall(); err != nil {
				app.logWithFields().Error()
			}
		}(app, &wg)
	}

	log.Debugf("%s: waiting for all applications uninstall", name)

	wg.Wait()

	h.Repos[repoIndex] = h.Repos[len(h.Repos)-1]
	h.Repos = h.Repos[:len(h.Repos)-1]

	return nil
}

func (h *RepositoryHandler) GetApplication(name string, url string) *Application {
	for _, r := range h.Repos {
		for _, a := range r.Apps {
			if a.Name == name && a.URL == url {
				return a
			}
		}
	}

	return nil
}
