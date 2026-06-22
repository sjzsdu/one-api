APP_NAME ?= one-api
WEB_DIR := web/default
GOFLAGS ?=
NPM_INSTALL ?= npm install --legacy-peer-deps
LDFLAGS ?= -s -w
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=
INSTALL_PATH := $(DESTDIR)$(BINDIR)/$(APP_NAME)

.PHONY: all build frontend backend install clean

all: build

build: frontend backend

frontend:
	@cd $(WEB_DIR) && $(NPM_INSTALL) && npm run build

backend:
	@go mod download
	@go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(APP_NAME)

install: build
	@if [ ! -w "$(DESTDIR)$(BINDIR)" ] && [ "$(DESTDIR)$(BINDIR)" = "/usr/local/bin" ]; then \
		echo "$(DESTDIR)$(BINDIR) is not writable. Run 'sudo make install' or use 'make install PREFIX=$$HOME/.local'."; \
		exit 1; \
	fi
	@install -d "$(DESTDIR)$(BINDIR)"
	@install -m 755 "$(APP_NAME)" "$(INSTALL_PATH)"

clean:
	@rm -rf web/build/default $(APP_NAME)
