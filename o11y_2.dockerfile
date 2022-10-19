FROM golang:1.16

ENV GRPC_GO_LOG_SEVERITY_LEVEL info
ENV GRPC_GO_LOG_VERBOSITY_LEVEL 2

RUN mkdir -p /build/install/examples/bin

ADD examples/build/install/examples/bin /build/install/examples/bin

ARG builddate
ENV IMAGE_DATE=$builddate

CMD ["/bin/sleep","inf"]

EXPOSE 8000

################################################
# jove
RUN\
  apt-get update &&\
  apt-get install -y jove &&\
  apt-get clean