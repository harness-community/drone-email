export GOPATH=""
# disable cgo
export CGO_ENABLED=0
# INC-2000: compile the shipped Windows binary with patched Go
export GOTOOLCHAIN=go1.26.10
set -e
set -x

# windows
GOOS=windows go build -o release/windows/amd64/drone-email.exe
