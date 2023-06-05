package util

import "errors"

var (
	NoErrAlreadyUpTodate         = errors.New("service is busy")
	ErrFailedToPrepareKnownHosts = errors.New("failed to prepare known hosts")
	ErrRepositoryAlreadyExist    = errors.New("repository already exist")
	ErrFailedToCreateRepository  = errors.New("failed to create repository")
	ErrRepositoryConfigNotFound  = errors.New("repository config not found")
	ErrApplicationAlreadyExist   = errors.New("application already exist")
	ErrApplicationNotFound       = errors.New("application not found")
)
