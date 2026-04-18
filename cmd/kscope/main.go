package main

import (
	"flag"
	"log"

	"github.com/purplespacecat/kscope/internal/server"
)

func main() {
	port := flag.String("port", "8080", "HTTP listen port")
	flag.Parse()

	s := server.New()
	log.Printf("kscope listening on :%s", *port)
	if err := s.Run(*port); err != nil {
		log.Fatal(err)
	}
}
