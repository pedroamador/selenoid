package service

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/aerokube/selenoid/config"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// Docker - docker container manager
type Docker struct {
	IP               string
	Client           *client.Client
	Service          *config.Browser
	LogConfig        *container.LogConfig
	ScreenResolution string
}

// StartWithCancel - Starter interface implementation
func (docker *Docker) StartWithCancel() (*url.URL, func(), error) {
	port, err := nat.NewPort("tcp", docker.Service.Port)
	if err != nil {
		return nil, nil, err
	}
	ctx := context.Background()
	log.Println("Creating Docker container", docker.Service.Image, "...")
	env := []string{
		fmt.Sprintf("TZ=%s", time.Local),
		fmt.Sprintf("SCREEN_RESOLUTION=%s", docker.ScreenResolution),
	}
	resp, err := docker.Client.ContainerCreate(ctx,
		&container.Config{
			Hostname:     "localhost",
			Image:        docker.Service.Image.(string),
			Env:          env,
			ExposedPorts: map[nat.Port]struct{}{port: {}},
		},
		&container.HostConfig{
			AutoRemove: true,
			PortBindings: nat.PortMap{
				port: []nat.PortBinding{{HostIP: "0.0.0.0"}},
			},
			LogConfig:  *docker.LogConfig,
			Tmpfs:      docker.Service.Tmpfs,
			ShmSize:    268435456,
			Privileged: true,
		},
		&network.NetworkingConfig{}, "")
	if err != nil {
		return nil, nil, fmt.Errorf("create container: %v", err)
	}
	containerStartTime := time.Now()
	log.Println("[STARTING_CONTAINER]")
	err = docker.Client.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		removeContainer(ctx, docker.Client, resp.ID)
		return nil, nil, fmt.Errorf("start container: %v", err)
	}
	log.Printf("[CONTAINER_STARTED] [%s] [%v]\n", resp.ID, time.Since(containerStartTime))
	stat, err := docker.Client.ContainerInspect(ctx, resp.ID)
	if err != nil {
		removeContainer(ctx, docker.Client, resp.ID)
		return nil, nil, fmt.Errorf("inspect container %s: %s", resp.ID, err)
	}
	_, ok := stat.NetworkSettings.Ports[port]
	if !ok {
		removeContainer(ctx, docker.Client, resp.ID)
		return nil, nil, fmt.Errorf("no bingings available for %v", port)
	}
	numBundings := len(stat.NetworkSettings.Ports[port])
	if numBundings != 1 {
		removeContainer(ctx, docker.Client, resp.ID)
		return nil, nil, fmt.Errorf("wrong number of port bindings: %d", numBundings)
	}
	addr := stat.NetworkSettings.Ports[port][0]
	if docker.IP == "" {
		_, err = os.Stat("/.dockerenv")
		if err != nil {
			addr.HostIP = "127.0.0.1"
		} else {
			addr.HostIP = stat.NetworkSettings.IPAddress
			addr.HostPort = docker.Service.Port
		}
	} else {
		addr.HostIP = docker.IP
	}
	host := fmt.Sprintf("http://%s:%s%s", addr.HostIP, addr.HostPort, docker.Service.Path)
	serviceStartTime := time.Now()
	err = wait(host, 30*time.Second)
	if err != nil {
		removeContainer(ctx, docker.Client, resp.ID)
		return nil, nil, err
	}
	log.Printf("[SERVICE_STARTED] [%s] [%v]\n", resp.ID, time.Since(serviceStartTime))
	u, _ := url.Parse(host)
	log.Println("proxying requests to:", host)
	return u, func() { removeContainer(ctx, docker.Client, resp.ID) }, nil
}

func removeContainer(ctx context.Context, cli *client.Client, id string) {
	log.Printf("[REMOVE_CONTAINER] [%s]\n", id)
	err := cli.ContainerRemove(ctx, id, types.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if err != nil {
		log.Println("error: unable to remove container", id, err)
		return
	}
	log.Printf("[CONTAINER_REMOVED] [%s]\n", id)
}
