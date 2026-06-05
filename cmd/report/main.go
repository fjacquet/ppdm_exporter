// Command report captures PowerProtect Data Manager backup history into PostgreSQL
// for assurance reporting (durable history; Grafana + branded reports read it).
package main

import "fmt"

var version = "dev"

func main() { fmt.Printf("report %s\n", version) }
