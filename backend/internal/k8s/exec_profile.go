package k8s

import (
	"path/filepath"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// applyAWSProfile points an AWS exec plugin at a named profile, in memory
// only; the kubeconfig on disk keeps whatever it said. For the AWS CLI it also
// passes --profile, because AWS_PROFILE alone loses to AWS_ACCESS_KEY_ID
// inherited from the process environment.
func applyAWSProfile(exec *clientcmdapi.ExecConfig, profile string) {
	if exec == nil || profile == "" {
		return
	}

	env := exec.Env[:0:0]
	for _, e := range exec.Env {
		if e.Name == "AWS_PROFILE" || e.Name == "AWS_DEFAULT_PROFILE" {
			continue
		}
		env = append(env, e)
	}
	exec.Env = append(env, clientcmdapi.ExecEnvVar{Name: "AWS_PROFILE", Value: profile})

	if filepath.Base(exec.Command) != "aws" {
		return
	}
	args := make([]string, 0, len(exec.Args)+2)
	for i := 0; i < len(exec.Args); i++ {
		if exec.Args[i] == "--profile" {
			i++
			continue
		}
		args = append(args, exec.Args[i])
	}
	exec.Args = append(args, "--profile", profile)
}
