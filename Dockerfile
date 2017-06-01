#create an image that build and run the auth service
FROM golang:1.8-alpine

LABEL name="imqs/auth"

# setup GOPATH and friends
#
# TECHNICALLY, you don't have to do these three cmds as the
# golang:alpine image actually uses this same directory structure and
# already has $GOPATH set to this same structure.  You could just
# remove these two lines and everything below should continue to work.

RUN  mkdir -p /go/src \
  && mkdir -p /go/bin \
  && mkdir -p /go/pkg
ENV GOPATH=/go
ENV PATH=$GOPATH/bin:$PATH

# now copy your app to the proper build path
RUN mkdir -p $GOPATH/src/github.com/IMQS/imqsauth
ADD . $GOPATH/src/github.com/IMQS/imqsauth

WORKDIR $GOPATH/src/github.com/IMQS/imqsauth

#install imqsauth to GOBIN (defaults to $GOPATH/bin) imqsauth is now available on your shell
RUN go-wrapper install    # "go install -v /go/src/github.com/IMQS/imqsauth/"


CMD ["imqsauth", "-nosvc", "run"]