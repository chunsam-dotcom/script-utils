#!/bin/bash

CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -w' -o wol ./wol.go
CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -w' -o md-graph ./md-graph.go
CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -w' -o java_analyzer ./java_analyzer.go
