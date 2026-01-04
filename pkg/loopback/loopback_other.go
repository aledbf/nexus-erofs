//go:build !linux

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

package loopback

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/v2/core/mount"
)

// ErofsMountHandler handles mounting EROFS filesystems with loop device support.
type ErofsMountHandler struct{}

// NewErofsMountHandler creates a new EROFS mount handler.
func NewErofsMountHandler() *ErofsMountHandler {
	return &ErofsMountHandler{}
}

// Mount is not supported on non-Linux platforms.
func (h *ErofsMountHandler) Mount(ctx context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	return mount.ActiveMount{}, fmt.Errorf("EROFS mount handler is only supported on Linux")
}

// Unmount is not supported on non-Linux platforms.
func (h *ErofsMountHandler) Unmount(ctx context.Context, path string) error {
	return fmt.Errorf("EROFS mount handler is only supported on Linux")
}
