#!/bin/bash

CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -w' -o ./wol.go
CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -w' -o ./md-graph.go
CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -w' -o ./java_analyzer.go
CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -w' -o ./blind-drop-t.go
CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-s -w' -o ./blind-drop blind-drop.go
