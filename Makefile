.NOTPARALLEL:

TARGET_MODULE := router
GO_BUILD_ENV := CGO_ENABLED=0
SHELL := /bin/dash

build:
	env $(GO_BUILD_ENV) go build

update_deps:
	go get -t -u ./... && go mod tidy && go mod vendor

test:
	go test -race $$(go list ./...)