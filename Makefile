NAME=swift-sftp
BINDIR=bin
GOARCH=amd64
VERSION=$(shell cat -e VERSION)

all: clean windows darwin linux

windows:
	GOOS=$@ GOARCH=$(GOARCH) CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BINDIR)/$@/$(NAME)-$(VERSION)/$(NAME).exe
	cp misc/swift-sftp.conf bin/$@

darwin linux: 
	$(eval BUILD_DIR := $(BINDIR)/$@/$(NAME)-$(VERSION))
	GOOS=$@ GOARCH=$(GOARCH) CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(NAME)
	cp misc/swift-sftp.conf $(BUILD_DIR)
	cd $(BUILD_DIR)/../; tar zcf $(NAME)-$(VERSION)-$@.$(GOARCH).tgz $(NAME)-$(VERSION)

clean:
	rm -rf $(BINDIR)

test:
	env ENV=test go test -cover -race -v
