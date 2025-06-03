package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"strings"
	"time"
)

var bashDockerImageTag string

func buildBashDockerIfNeeded() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %s", err.Error())
	}
	baseImage := "ubuntu:noble"
	cmdsToExecute := []string{
		"apt-get update",
		"apt-get install -y git tree ripgrep curl",
		"curl -sSL https://go.dev/dl/go1.24.3.linux-amd64.tar.gz | tar -C /usr/local -xzf -",
		"echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh",
		"chmod +x /etc/profile.d/go.sh && source /etc/profile.d/go.sh",
		"go mod tidy",
	}
	cmdHash := fnv.New64a()
	for _, cmd := range cmdsToExecute {
		if _, err := cmdHash.Write([]byte(cmd)); err != nil {
			return fmt.Errorf("error writing command to hash: %w", err)
		}
	}
	bashDockerImageTag = "ikm-bash:" + fmt.Sprintf("%x", cmdHash.Sum64())
	cmd := exec.Command("docker", "images", "-q", bashDockerImageTag)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("error checking for existing image: %w", err)
	}
	if len(out) > 0 {
		return nil
	}
	fmt.Printf("docker image %s not found, building\n", bashDockerImageTag)
	tempContainerName := "ikm-" + fmt.Sprintf("%x", time.Now().Unix())
	dockerCmds := [][]string{
		{"docker", "pull", baseImage},
		{"docker", "run", "-v", fmt.Sprintf(".:%s", cwd), "-w", cwd, "--name", tempContainerName, baseImage, "/bin/bash", "-c", strings.Join(cmdsToExecute, " && ")},
		{"docker", "commit", tempContainerName, bashDockerImageTag},
		{"docker", "rm", tempContainerName},
	}
	for _, cmd := range dockerCmds {
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			fmt.Println(string(out))
			return fmt.Errorf("error running docker command %v: %w", cmd, err)
		}
	}
	fmt.Printf("docker image %s built successfully\n", bashDockerImageTag)
	return nil
}

func runInBashDocker(ctx context.Context, cmd string) (int, string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to get current working directory: %s", err.Error())
	}
	dockerCmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", fmt.Sprintf(".:%s:ro", cwd),
		"-w", cwd,
		"--network", "none",
		bashDockerImageTag,
		"bash", "-l", "-c", cmd)
	var stdoutBuf, stderrBuf bytes.Buffer
	dockerCmd.Stdout = &stdoutBuf
	dockerCmd.Stderr = &stderrBuf
	if err := dockerCmd.Start(); err != nil {
		return 0, "", "", fmt.Errorf("error executing command: %w", err)
	}
	err = dockerCmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdoutBuf.String(), stderrBuf.String(), nil
	}
	if err != nil {
		return 0, "", "", fmt.Errorf("error executing command: %w", err)
	}
	return dockerCmd.ProcessState.ExitCode(), stdoutBuf.String(), stderrBuf.String(), nil
}
