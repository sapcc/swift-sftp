FROM golang:alpine AS builder

RUN apk update && apk add --no-cache git make
WORKDIR $GOPATH/src/github.com/sapcc/swift-sftp

COPY . .
RUN make setup && make linux

FROM scratch
COPY --from=builder /go/src/github.com/sapcc/swift-sftp/bin/linux/swift-sftp-1.1.3/swift-sftp  /go/bin/swift-sftp
ENTRYPOINT ["/go/bin/swift-sftp"]

