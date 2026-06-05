// Command ppdm_exporter is a Prometheus + OTLP exporter for Dell PowerProtect Data Manager.
package main

import "fmt"

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Printf("ppdm_exporter %s\n", version)
}
