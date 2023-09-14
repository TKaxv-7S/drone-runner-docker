// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"github.com/drone-runners/drone-runner-docker/engine/experimental/engine"
	"github.com/drone-runners/drone-runner-docker/internal/docker/image"

	harness "github.com/drone/spec/dist/go"
)

func createStepPlugin(src *harness.Step, spec *harness.StepBackground) *engine.Step {
	dst := &engine.Step{
		ID:         random(),
		Name:       src.Name,
		Image:      image.Expand(spec.Image),
		Command:    spec.Args,
		Entrypoint: []string{spec.Entrypoint},
		Detach:     true,
		// TODO re-enable
		// DependsOn:    src.DependsOn,
		// DNS:          spec.DNS,
		// TODO re-enable
		// DNSSearch:    spec.DNSSearch,
		Envs: spec.Envs,
		// TODO re-enable
		// ExtraHosts:   spec.ExtraHosts,
		IgnoreStderr: false,
		IgnoreStdout: false,
		Network:      spec.Network,
		Privileged:   spec.Privileged,
		// TODO re-enable
		// Pull:         convertPullPolicy(src.Pull),
		User: spec.User,
		// TODO re-enable
		// Secrets:      convertSecretEnv(src.Environment),
		// TODO re-enable
		// ShmSize:    int64(spec.ShmSize),
		// TODO re-enable
		// WorkingDir: spec.WorkingDir,

		//
		//
		//

		Networks: nil, // set in compiler.go
		Volumes:  nil, // set below
		Devices:  nil, // see below
		// Resources:    toResources(src), // TODO
	}

	// TODO re-enable
	// set container limits
	// if v := int64(src.MemLimit); v > 0 {
	// 	dst.MemLimit = v
	// }
	// if v := int64(src.MemSwapLimit); v > 0 {
	// 	dst.MemSwapLimit = v
	// }

	// appends the volumes to the container def.
	for _, vol := range spec.Mount {
		dst.Volumes = append(dst.Volumes, &engine.VolumeMount{
			Name: vol.Name,
			Path: vol.Path,
		})
	}

	// TODO re-enable
	// // set the pipeline step run policy. steps run on
	// // success by default, but may be optionally configured
	// // to run on failure.
	// if isRunAlways(src) {
	// 	dst.RunPolicy = RunAlways
	// } else if isRunOnFailure(src) {
	// 	dst.RunPolicy = RunOnFailure
	// }

	// TODO re-enable
	// // set the pipeline failure policy. steps can choose
	// // to ignore the failure, or fail fast.
	// switch src.Failure {
	// case "ignore":
	// 	dst.ErrPolicy = ErrIgnore
	// case "fast", "fast-fail", "fail-fast":
	// 	dst.ErrPolicy = ErrFailFast
	// }

	return dst
}
