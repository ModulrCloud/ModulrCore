package main

import (
	"errors"
	"fmt"
	"net"
)

func findAvailableGeneratedBasePort(startBasePort, stride int, coreCount, anchorCount int) (int, error) {
	if stride < 3004 {
		return 0, errors.New("port stride must be at least 3004")
	}
	for basePort := startBasePort; basePort <= 65535; basePort += stride {
		if err := ensureGeneratedPortsAvailable(basePort, coreCount, anchorCount); err == nil {
			return basePort, nil
		}
	}
	return 0, fmt.Errorf("could not find a free generated port range from base %d with stride %d", startBasePort, stride)
}

func ensureGeneratedPortsAvailable(basePort, coreCount, anchorCount int) error {
	ports := generatedNodePorts(basePort, coreCount, anchorCount)
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("generated port %d from base-port %d is outside valid TCP range", port, basePort)
		}
		if err := checkPortAvailable(port); err != nil {
			return fmt.Errorf("generated port %d from base-port %d is unavailable: %w", port, basePort, err)
		}
	}
	return nil
}

func generatedNodePorts(basePort, coreCount, anchorCount int) []int {
	ports := make([]int, 0, coreCount*2+anchorCount*2)
	for idx := 0; idx < coreCount; idx++ {
		ports = append(ports, basePort+idx, basePort+1000+idx)
	}
	for idx := 0; idx < anchorCount; idx++ {
		ports = append(ports, basePort+2000+idx, basePort+3000+idx)
	}
	return ports
}

func checkPortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	_ = listener.Close()

	return nil
}
