GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
GOPROXY ?= $(shell go env GOPROXY)
GIT_VERSION ?= $(shell git describe --dirty --tags --match='v*')
VERSION ?= $(GIT_VERSION)
LDFLAGS ?= -w -s -X k8s.io/component-base/version.gitVersion=$(VERSION) -X main.gitVersion=$(VERSION)

.PHONY: aws-cloud-controller-manager-tests-ext
aws-cloud-controller-manager-tests-ext:
	 pushd tests/aws-cloud-controller-manager-tests-ext && \
	 GO111MODULE=on CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) GOPROXY=$(GOPROXY) go build \
		-trimpath \
		-ldflags="$(LDFLAGS)" \
		-o=../../aws-cloud-controller-manager-tests-ext .

.PHONY: image
image:
	podman build -f Dockerfile.openshift \
		--authfile $(PULL_SECRET_FILE) \
		--build-arg VERSION=$(VERSION) \
		-t aws-cloud-controller-manager:$(VERSION) .
