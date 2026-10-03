package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"context"
)

type DockerContainer struct {
	Id     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

func main() {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/var/run/docker.sock")
			},
		},
	}
	resp, err := client.Get("http://localhost/v1.41/containers/json?all=true")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var containers []DockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		panic(err)
	}

	// 1. Find the project of service-monitor (or fallback to 'backend' or 'adatrack_gps_prod')
	targetProject := ""
	for _, c := range containers {
		svc := c.Labels["com.docker.compose.service"]
		if svc == "service-monitor" || svc == "service-websocket" || svc == "ingestion-tcp" {
			targetProject = c.Labels["com.docker.compose.project"]
			if targetProject != "" {
				break
			}
		}
	}
	
	fmt.Println("Detected Target Project:", targetProject)

	for _, c := range containers {
		proj := c.Labels["com.docker.compose.project"]
		if proj == targetProject && targetProject != "" {
			fmt.Printf("KEEP: %v (Service: %s)\n", c.Names[0], c.Labels["com.docker.compose.service"])
		}
	}
}
