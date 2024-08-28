.NOTPARALLEL:

TARGET_MODULE := router
GO_BUILD_ENV := CGO_ENABLED=0
SHELL := /bin/dash

build:
	env $(GO_BUILD_ENV) go build