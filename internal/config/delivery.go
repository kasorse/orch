// This file defines the optional delivery section (specs/delivery/SPEC.md §8): where a worktree
// run's branch is pushed and which branch a merge request targets when HEAD names none.
package config

import "strings"

// DefaultDeliveryRemote is delivery.remote when the key is absent.
const DefaultDeliveryRemote = "origin"

// DeliveryConfig is the delivery section. Both keys are optional, and so is the section; there is
// no token and no URL: the push uses the operator's git credentials.
type DeliveryConfig struct {
	// Remote is the git remote the branch is pushed to. Read it through RemoteName.
	Remote string `yaml:"remote"`
	// TargetBranch is the merge request's target when HEAD is detached (a CI checkout).
	TargetBranch string `yaml:"target_branch"`
}

// RemoteName is delivery.remote, origin when absent.
func (d DeliveryConfig) RemoteName() string {
	if r := strings.TrimSpace(d.Remote); r != "" {
		return r
	}
	return DefaultDeliveryRemote
}
