FROM golang:alpine AS builder

RUN apk update && apk add --no-cache git make ca-certificates
WORKDIR $GOPATH/src/github.com/sapcc/swift-sftp

COPY . .
RUN make setup && make linux

FROM busybox
LABEL source_repository="https://github.com/sapcc/swift-sftp"
ENV PATH=/bin
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /go/src/github.com/sapcc/swift-sftp/bin/linux/swift-sftp-1.1.3/swift-sftp /go/bin/swift-sftp
ENTRYPOINT ["/go/bin/swift-sftp"]

