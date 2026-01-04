/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package erofsutils

import (
	"testing"

	"github.com/containerd/containerd/v2/core/mount"

	// Import testutil to register the -test.root flag
	_ "github.com/aledbf/nexuserofs/internal/testutil"
)

func TestMountsToLayer(t *testing.T) {
	tests := []struct {
		name        string
		mounts      []mount.Mount
		expectError bool
	}{
		{
			name: "mkfs type mount",
			mounts: []mount.Mount{
				{Type: "mkfs/ext4", Source: "/some/path/layer.erofs"},
			},
			expectError: true, // No .erofslayer marker
		},
		{
			name: "bind mount without marker",
			mounts: []mount.Mount{
				{Type: "bind", Source: "/some/path/fs"},
			},
			expectError: true, // No .erofslayer marker
		},
		{
			name: "erofs mount without marker",
			mounts: []mount.Mount{
				{Type: "erofs", Source: "/some/path/layer.erofs"},
			},
			expectError: true, // No .erofslayer marker
		},
		{
			name: "overlay mount without marker",
			mounts: []mount.Mount{
				{Type: "overlay", Source: "overlay", Options: []string{"upperdir=/tmp/upper", "lowerdir=/tmp/lower"}},
			},
			expectError: true, // No .erofslayer marker
		},
		{
			name: "unsupported mount type",
			mounts: []mount.Mount{
				{Type: "tmpfs", Source: "tmpfs"},
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MountsToLayer(tc.mounts)
			if tc.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestSupportGenerateFromTar(t *testing.T) {
	// This test just verifies the function doesn't panic
	// The actual result depends on whether mkfs.erofs is installed
	supported, err := SupportGenerateFromTar()
	if err != nil {
		t.Logf("mkfs.erofs not available: %v", err)
		return
	}
	t.Logf("mkfs.erofs tar support: %v", supported)
}
