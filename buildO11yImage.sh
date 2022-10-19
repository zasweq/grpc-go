#!/bin/bash
set -e

IMAGENAME=o11y-examples
TAG=1.1
PROJECTID=grpctesting

echo Building ${IMAGENAME}:${TAG}

docker build --build-arg builddate="$(date)" --no-cache -t o11y/${IMAGENAME}:${TAG} -f o11y_2.dockerfile .

docker tag o11y/${IMAGENAME}:${TAG} ${PROJECTID}/${IMAGENAME}:${TAG}

docker push ${PROJECTID}/${IMAGENAME}:${TAG}