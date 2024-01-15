#!/bin/bash -x

source_path=_output/SOURCES

mkdir -p ${source_path}

# Getting version from args
version=${1:-4.14.0}

# Trick to make sure we'll install this RPM later on, not the one from the repo.
release=999999999999

# NOTE:        rpmbuild requires that inside the tar there will be a
#              ${service}-${version} directory, hence this --transform option.
#              We exclude .git as rpmbuild will do its own `git init`.
#              Excluding .tox is convenient for local builds.
tar -czvf ${source_path}/ecr-credential-provider.tar.gz --exclude=.git --exclude=.tox --transform "flags=r;s|\.|ecr-credential-provider-${version}|" .


rpmbuild -ba -D "_version $version" -D "_release $release" -D "_topdir `pwd`/_output" ecr-credential-provider.spec
# TODO: We might need to change this
createrepo _output/RPMS/noarch
