// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package compiler

import (
	"context"
	"errors"
	"strings"

	"github.com/drone-runners/drone-runner-docker/engine/experimental/engine"
	"github.com/drone-runners/drone-runner-docker/engine/resource"
	"github.com/drone-runners/drone-runner-docker/internal/docker/image"

	"github.com/drone/drone-go/drone"
	"github.com/drone/runner-go/clone"
	"github.com/drone/runner-go/container"
	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/labels"
	"github.com/drone/runner-go/registry"
	"github.com/drone/runner-go/secret"

	harness "github.com/drone/spec/dist/go"

	"github.com/dchest/uniuri"
)

// random generator function
var random = func() string {
	return "drone-" + uniuri.NewLen(20)
}

// Privileged provides a list of plugins that execute
// with privileged capabilities in order to run Docker
// in Docker.
var Privileged = []string{
	"plugins/docker",
	"plugins/acr",
	"plugins/ecr",
	"plugins/gcr",
	"plugins/heroku",
}

// Resources defines container resource constraints. These
// constraints are per-container, not per-pipeline.
type Resources struct {
	Memory     int64
	MemorySwap int64
	CPUQuota   int64
	CPUPeriod  int64
	CPUShares  int64
	CPUSet     []string
	ShmSize    int64
}

// Tmate defines tmate settings.
type Tmate struct {
	Image          string
	Enabled        bool
	Server         string
	Port           string
	RSA            string
	ED25519        string
	AuthorizedKeys string
}

// CompilerImpl compiles the Yaml configuration file to an
// intermediate representation optimized for simple execution.
type CompilerImpl struct {
	// Environ provides a set of environment variables that
	// should be added to each pipeline step by default.
	Environ provider.Provider

	// Labels provides a set of labels that should be added
	// to each container by default.
	Labels map[string]string

	// Privileged provides a list of docker images that
	// are always privileged.
	Privileged []string

	// Networks provides a set of networks that should be
	// attached to each pipeline container.
	Networks []string

	// NetworkOpts provides a set of network options that
	// are used when creating the docker network.
	NetworkOpts map[string]string

	// NetrcCloneOnly instructs the compiler to only inject
	// the netrc file into the clone step.
	NetrcCloneOnly bool

	// Volumes provides a set of volumes that should be
	// mounted to each pipeline container.
	Volumes map[string]string

	// Clone overrides the default plugin image used
	// when cloning a repository.
	Clone string

	// Resources provides global resource constraints
	// applies to pipeline containers.
	Resources Resources

	// Tmate provides global configuration options for tmate
	// live debugging.
	Tmate Tmate

	// Secret returns a named secret value that can be injected
	// into the pipeline step.
	Secret secret.Provider

	// Registry returns a list of registry credentials that can be
	// used to pull private container images.
	Registry registry.Provider

	// Mount is an optional field that overrides the default
	// workspace volume and mounts to the host path
	Mount string
}

// Compile compiles the configuration file.
func (c *CompilerImpl) Compile(ctx context.Context, args Args) (*engine.Spec, error) {

	// extract the pipeline resource
	pipeline, ok := args.Config.Spec.(*harness.Pipeline)
	if !ok {
		return nil, errors.New("pipeline resource not found")
	}

	// extract the pipeline stage and stage spec
	var stage *harness.Stage
	for _, resource := range pipeline.Stages {
		// TODO match by name
		if resource.Name == args.Stage.Name {
			stage = resource
			break
		}
	}
	if stage == nil {
		return nil, errors.New("stage resource not found")
	}
	stageSpec, ok := stage.Spec.(*harness.StageCI)
	if !ok {
		return nil, errors.New("ci stage resource not found")
	}

	// extract the pipeline stage platform
	platform_ := new(harness.Platform)
	if v := stageSpec.Platform; v != nil {
		platform_ = v
	}

	// extract the clone. use the stage settings, else
	// fallback to the pipeline-level clone settings.
	clone_ := new(harness.Clone)
	if v := pipeline.Options; v != nil {
		if vv := v.Clone; vv != nil {
			clone_ = vv
		}
	}
	if v := stageSpec.Clone; v != nil {
		clone_ = &harness.Clone{
			Depth:    v.Depth,
			Disabled: v.Disabled,
			Insecure: v.Insecure,
			Trace:    v.Trace,
		}
	}

	// create system labels
	stageLabels := labels.Combine(
		c.Labels,
		labels.FromRepo(args.Repo),
		labels.FromBuild(args.Build),
		labels.FromStage(args.Stage),
		labels.FromSystem(args.System),
		labels.WithTimeout(args.Repo),
	)

	// create the workspace mount
	mount := &engine.VolumeMount{
		Name: "_workspace",
		Path: "/gitness", // TODO handle windows. c://gitness
	}

	// create the workspace volume
	volume := &engine.Volume{
		EmptyDir: &engine.VolumeEmptyDir{
			ID:     random(),
			Name:   mount.Name,
			Labels: stageLabels,
		},
	}

	// if the repository is mounted from a local volume,
	// we should replace the data volume with a host machine
	// volume declaration.
	if c.Mount != "" {
		volume.EmptyDir = nil
		volume.HostPath = &engine.VolumeHostPath{
			ID:     random(),
			Name:   mount.Name,
			Path:   c.Mount,
			Labels: stageLabels,
		}
	}

	spec := &engine.Spec{
		Network: engine.Network{
			ID:      random(),
			Labels:  stageLabels,
			Options: c.NetworkOpts,
		},
		Platform: engine.Platform{
			OS:   platform_.Os.String(),
			Arch: platform_.Arch.String(),
			// Variant: platform_.Variant,
			// Version: platform_.Version,
		},
		Volumes: []*engine.Volume{volume},
	}

	// // list the global environment variables
	// globals, _ := c.Environ.List(ctx, &provider.Request{
	// 	Build: args.Build,
	// 	Repo:  args.Repo,
	// })

	// create the default environment variables.
	envs := environ.Combine(
		// provider.ToMap(
		// 	provider.FilterUnmasked(globals),
		// ),
		args.Build.Params,
		stageSpec.Envs,
		environ.Proxy(),
		environ.System(args.System),
		environ.Repo(args.Repo),
		environ.Build(args.Build),
		environ.Stage(args.Stage),
		environ.Link(args.Repo, args.Build, args.System),
		clone.Environ(clone.Config{
			SkipVerify: clone_.Insecure,
			Trace:      clone_.Trace,
			User: clone.User{
				Name:  args.Build.AuthorName,
				Email: args.Build.AuthorEmail,
			},
		}),
	)

	// create network reference variables
	envs["DRONE_DOCKER_NETWORK_ID"] = spec.Network.ID

	// create the workspace variables
	envs["DRONE_WORKSPACE"] = "/gitness"
	envs["DRONE_WORKSPACE_BASE"] = "/gitness"
	envs["DRONE_WORKSPACE_PATH"] = ""

	// create volume reference variables
	if volume.EmptyDir != nil {
		envs["DRONE_DOCKER_VOLUME_ID"] = volume.EmptyDir.ID
	} else {
		envs["DRONE_DOCKER_VOLUME_PATH"] = volume.HostPath.Path
	}

	// create tmate variables
	if c.Tmate.Server != "" {
		envs["DRONE_TMATE_HOST"] = c.Tmate.Server
		envs["DRONE_TMATE_PORT"] = c.Tmate.Port
		envs["DRONE_TMATE_FINGERPRINT_RSA"] = c.Tmate.RSA
		envs["DRONE_TMATE_FINGERPRINT_ED25519"] = c.Tmate.ED25519

		if c.Tmate.AuthorizedKeys != "" {
			envs["DRONE_TMATE_AUTHORIZED_KEYS"] = c.Tmate.AuthorizedKeys
		}
	}

	// create the .netrc environment variables if not
	// explicitly disabled
	if c.NetrcCloneOnly == false {
		envs = environ.Combine(envs, environ.Netrc(args.Netrc))
	}

	// TODO re-enable
	// match := manifest.Match{
	// 	Action:   args.Build.Action,
	// 	Cron:     args.Build.Cron,
	// 	Ref:      args.Build.Ref,
	// 	Repo:     args.Repo.Slug,
	// 	Instance: args.System.Host,
	// 	Target:   args.Build.Deploy,
	// 	Event:    args.Build.Event,
	// 	Branch:   args.Build.Target,
	// }

	// create the clone step
	if clone_.Disabled == false {
		step := createClone(platform_, clone_)
		step.ID = random()
		step.Envs = environ.Combine(envs, step.Envs)
		step.WorkingDir = "/gitness"
		step.Labels = stageLabels
		step.Pull = engine.PullIfNotExists
		step.Volumes = append(step.Volumes, mount)
		spec.Steps = append(spec.Steps, step)

		// always set the .netrc file for the clone step.
		// note that environment variables are only set
		// if the .netrc file is not nil (it will always
		// be nil for public repositories).
		step.Envs = environ.Combine(step.Envs, environ.Netrc(args.Netrc))

		// if the clone image is customized, override
		// the default image.
		if c.Clone != "" {
			step.Image = c.Clone
		}

		// if the repository is mounted from a local
		// volume we should disable cloning.
		if c.Mount != "" {
			step.RunPolicy = engine.RunNever
		}
	}

	// create steps
	for _, src := range stageSpec.Steps {

		switch v := src.Spec.(type) {
		case *harness.StepExec:
			dst := createStep(src, v)
			dst.Envs = environ.Combine(envs, dst.Envs)
			dst.Volumes = append(dst.Volumes, mount)
			dst.Labels = stageLabels
			dst.WorkingDir = "/gitness"
			setupScript(dst, v.Run, platform_.Os.String())
			// TODO do we need this still?
			// setupWorkdir(src, dst, "/gitness")
			spec.Steps = append(spec.Steps, dst)

			// TODO re-enable
			// // if the pipeline step has unmet conditions the step is
			// // automatically skipped.
			// if !src.When.Match(match) {
			// 	// TODO
			// 	// dst.RunPolicy = runtime.RunNever
			// }

			// TODO re-enable
			// // if the pipeline step has an approved image, it is
			// // automatically defaulted to run with escalalated
			// // privileges.
			// if c.isPrivileged(src) {
			// 	dst.Privileged = true
			// }
		case *harness.StepPlugin:
		case *harness.StepTemplate:
		case *harness.StepBackground:

			dst := createStepBackground(src, v)
			dst.Envs = environ.Combine(envs, dst.Envs)
			dst.Volumes = append(dst.Volumes, mount)
			dst.Labels = stageLabels
			setupScript(dst, v.Run, platform_.Os.String())
			spec.Steps = append(spec.Steps, dst)

			// TODO re-enable
			// // if the pipeline step has unmet conditions the step is
			// // automatically skipped.
			// if !src.When.Match(match) {
			// 	// TODO
			// 	// dst.RunPolicy = runtime.RunNever
			// }

			// TODO re-enable
			// // if the pipeline step has an approved image, it is
			// // automatically defaulted to run with escalalated
			// // privileges.
			// if c.isPrivileged(src) {
			// 	dst.Privileged = true
			// }
		}

	}

	// create internal steps if build running in debug mode
	if c.Tmate.Enabled && args.Build.Debug && platform_.Os.String() != "windows" {
		// first we need to add an internal setup step to the pipeline
		// to copy over the tmate binary. Internal steps are not visible
		// to the end user.
		spec.Internal = append(spec.Internal, &engine.Step{
			ID:         random(),
			Labels:     stageLabels,
			Pull:       engine.PullIfNotExists,
			Image:      image.Expand(c.Tmate.Image),
			Entrypoint: []string{"/bin/drone-runner-docker"},
			Command:    []string{"copy"},
			Network:    "none",
		})

		// next we create a temporary volume to share the tmate binary
		// with the pipeline containers.
		for _, step := range append(spec.Steps, spec.Internal...) {
			step.Volumes = append(step.Volumes, &engine.VolumeMount{
				Name: "_addons",
				Path: "/usr/drone/bin",
			})
		}
		spec.Volumes = append(spec.Volumes, &engine.Volume{
			EmptyDir: &engine.VolumeEmptyDir{
				ID:     random(),
				Name:   "_addons",
				Labels: stageLabels,
			},
		})
	}

	// TODO re-enable
	// if isGraph(spec) == false {
	// 	configureSerial(spec)
	// } else if pipeline.Clone.Disable == false {
	// 	configureCloneDeps(spec)
	// } else if pipeline.Clone.Disable == true {
	// 	removeCloneDeps(spec)
	// }

	//
	// TODO re-enable, find matching secrets and inject
	//
	// for _, step := range spec.Steps {
	// 	for _, s := range step.Secrets {
	// 		secret, ok := c.findSecret(ctx, args, s.Name)
	// 		if ok {
	// 			s.Data = []byte(secret)
	// 		}
	// 	}
	// }

	// get registry credentials from registry plugins
	creds, err := c.Registry.List(ctx, &registry.Request{
		Repo:  args.Repo,
		Build: args.Build,
	})
	if err != nil {
		// TODO (bradrydzewski) return an error to the caller
		// if the provider returns an error.
	}

	//
	// TODO re-enable, get registry credentials from secrets
	//

	// // get registry credentials from secrets
	// for _, name := range pipeline.PullSecrets {
	// 	secret, ok := c.findSecret(ctx, args, name)
	// 	if ok {
	// 		parsed, err := auths.ParseString(secret)
	// 		if err == nil {
	// 			creds = append(parsed, creds...)
	// 		}
	// 	}
	// }

	for _, step := range append(spec.Steps, spec.Internal...) {
	STEPS:
		for _, cred := range creds {
			if image.MatchHostname(step.Image, cred.Address) {
				step.Auth = &engine.Auth{
					Address:  cred.Address,
					Username: cred.Username,
					Password: cred.Password,
				}
				break STEPS
			}
		}
	}

	// // HACK: append masked global variables to secrets
	// // this ensures the environment variable values are
	// // masked when printed to the console.
	// masked := provider.FilterMasked(globals)
	// for _, step := range spec.Steps {
	// 	for _, g := range masked {
	// 		step.Secrets = append(step.Secrets, &engine.Secret{
	// 			Name: g.Name,
	// 			Data: []byte(g.Data),
	// 			Mask: g.Mask,
	// 			Env:  g.Name,
	// 		})
	// 	}
	// }

	// append global resource limits to steps
	for _, step := range spec.Steps {
		// the resource limits defined in the yaml currently
		// take precedence over global values. This is something
		// we should re-think in a future release.
		if step.MemSwapLimit == 0 {
			step.MemSwapLimit = c.Resources.MemorySwap
		}
		if step.MemLimit == 0 {
			step.MemLimit = c.Resources.Memory
		}
		if step.ShmSize == 0 {
			step.ShmSize = c.Resources.ShmSize
		}
		step.CPUPeriod = c.Resources.CPUPeriod
		step.CPUQuota = c.Resources.CPUQuota
		step.CPUShares = c.Resources.CPUShares
		step.CPUSet = c.Resources.CPUSet
	}

	// append global networks to the steps.
	// append step labels to steps.
	for n, step := range spec.Steps {
		step.Networks = append(step.Networks, c.Networks...)
		step.Labels = labels.Combine(step.Labels, labels.FromStep(&drone.Step{
			Number: n + 1,
			Name:   step.Name,
		}))
	}

	// append global volumes to the steps.
	for k, v := range c.Volumes {
		id := random()
		ro := strings.HasSuffix(v, ":ro")
		v = strings.TrimSuffix(v, ":ro")
		volume := &engine.Volume{
			HostPath: &engine.VolumeHostPath{
				ID:       id,
				Name:     id,
				Path:     k,
				ReadOnly: ro,
			},
		}
		spec.Volumes = append(spec.Volumes, volume)
		for _, step := range spec.Steps {
			mount := &engine.VolumeMount{
				Name: id,
				Path: v,
			}
			step.Volumes = append(step.Volumes, mount)
		}
	}

	// TODO re-enable volumes
	// // append volumes
	// for _, v := range pipeline.Volumes {
	// 	id := random()
	// 	src := new(engine.Volume)
	// 	if v.EmptyDir != nil {
	// 		src.EmptyDir = &engine.VolumeEmptyDir{
	// 			ID:        id,
	// 			Name:      v.Name,
	// 			Medium:    v.EmptyDir.Medium,
	// 			SizeLimit: int64(v.EmptyDir.SizeLimit),
	// 			Labels:    stageLabels,
	// 		}
	// 	} else if v.HostPath != nil {
	// 		src.HostPath = &engine.VolumeHostPath{
	// 			ID:   id,
	// 			Name: v.Name,
	// 			Path: v.HostPath.Path,
	// 		}
	// 	} else {
	// 		continue
	// 	}
	// 	spec.Volumes = append(spec.Volumes, src)
	// }

	return spec, nil
}

func (c *CompilerImpl) isPrivileged(step *resource.Step) bool {
	// privileged-by-default containers are only
	// enabled for plugins steps that do not define
	// commands, command, or entrypoint.
	if len(step.Commands) > 0 {
		return false
	}
	if len(step.Command) > 0 {
		return false
	}
	if len(step.Entrypoint) > 0 {
		return false
	}
	if len(step.Volumes) > 0 {
		return false
	}

	// privileged-by-default mode is disabled if the
	// pipeline step mounts a volume restricted for
	// internal use only.
	// note: this is deprecated.
	for _, mount := range step.Volumes {
		if container.IsRestrictedVolume(mount.MountPath) {
			return false
		}
	}
	// privileged-by-default mode is disabled if the
	// pipeline step attempts to use an environment
	// variable restricted for internal use only.
	if isRestrictedVariable(step.Environment) {
		return false
	}
	// if the container image matches any image
	// in the whitelist, return true.
	for _, img := range c.Privileged {
		a := img
		b := step.Image
		if image.Match(a, b) {
			return true
		}
	}
	return false
}

// // helper function attempts to find and return the named secret.
// // from the secret provider.
// func (c *Compiler) findSecret(ctx context.Context, args runtime.CompilerArgs, name string) (s string, ok bool) {
// 	if name == "" {
// 		return
// 	}

// 	// source secrets from the global secret provider
// 	// and the repository secret provider.
// 	provider := secret.Combine(
// 		args.Secret,
// 		c.Secret,
// 	)

// 	// TODO return an error to the caller if the provider
// 	// returns an error.
// 	found, _ := provider.Find(ctx, &secret.Request{
// 		Name:  name,
// 		Build: args.Build,
// 		Repo:  args.Repo,
// 		Conf:  args.Manifest,
// 	})
// 	if found == nil {
// 		return
// 	}
// 	return found.Data, true
// }
