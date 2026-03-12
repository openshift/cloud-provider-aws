GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
GOPROXY ?= $(shell go env GOPROXY)
GIT_VERSION ?= $(shell git describe --dirty --tags --match='v*')
VERSION ?= $(GIT_VERSION)
LDFLAGS ?= -w -s -X k8s.io/component-base/version.gitVersion=$(VERSION) -X main.gitVersion=$(VERSION)

.PHONY: image
image:
	podman build -f Dockerfile.openshift \
		--authfile $(PULL_SECRET_FILE) \
		--build-arg VERSION=$(VERSION) \
		-t aws-cloud-controller-manager:$(VERSION) .
