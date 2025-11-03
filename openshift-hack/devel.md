# OpenShift hacking

## OpenShift Tests Extension (OTE) binary

### Building

Chose one of the steps below to build the OTE binary from root of the project.

To build the OTE binary you can run:

```sh
make -f openshift-hack/Makefile aws-cloud-controller-manager-tests-ext
```

To build the container image you can run:

```sh
podman build --authfile $PULL_SECRET_FILE -f Dockerfile.openshift -t ccm-local:devel .

# OR

make -f openshift-hack/Makefile image
```

where:

- `$PULL_SECRET_FILE` is the path to the registry credentials ('pull-secret')

### Using OTE

List the existing tests:

```sh
./aws-cloud-controller-manager-tests-ext list tests | jq .[].name
```

List suites:

```sh
./aws-cloud-controller-manager-tests-ext list suites | jq .[].name
```

Run a specific test:

```sh
./aws-cloud-controller-manager-tests-ext run-test  -n "[cloud-provider-aws-e2e] loadbalancer CLB internal should be reachable with hairpinning traffic [Suite:openshift/conformance/parallel]"
```
