SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.ONESHELL:        # use a single shell for commands instead a new shell per line
.DELETE_ON_ERROR: # delete output files when make rule fails
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

.PHONY: all
all: test lint

.PHONY: test
test:
	go test -race

.PHONY: lint
lint:
	golangci-lint run
