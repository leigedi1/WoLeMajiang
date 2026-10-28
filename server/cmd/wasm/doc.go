//go:build !(js && wasm)

// 占位入口: 让 `go build ./...` / `go vet ./...` 在非 js/wasm 平台下
// 仍能通过(真正的实现见 main.go, 仅在 GOOS=js GOARCH=wasm 下编译)。
package main

func main() {}
