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
	"errors"
	"os"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/errdefs"
	"golang.org/x/sys/unix"
)

var forceloop bool

// ErofsMountHandler handles mounting EROFS filesystems with loop device support.
type ErofsMountHandler struct{}

// NewErofsMountHandler creates a new EROFS mount handler.
func NewErofsMountHandler() *ErofsMountHandler {
	return &ErofsMountHandler{}
}

// Mount mounts an EROFS filesystem at the specified mount point.
// It handles loop device setup for EROFS images.
func (h *ErofsMountHandler) Mount(ctx context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	if m.Type != "erofs" {
		return mount.ActiveMount{}, errdefs.ErrNotImplemented
	}

	// Remove the loop option which is specified if the dedicated mount handler is available
	for i, v := range m.Options {
		if v == "loop" {
			m.Options = append(m.Options[:i], m.Options[i+1:]...)
			break
		}
	}

	if err := os.MkdirAll(mp, 0700); err != nil {
		return mount.ActiveMount{}, err
	}

	var err error = unix.ENOTBLK
	if !forceloop {
		// Try to use file-backed mount feature if available (Linux 6.12+) first
		err = m.Mount(mp)
	}
	if errors.Is(err, unix.ENOTBLK) {
		var loops []*os.File

		// Never try to mount with raw files anymore if tried
		forceloop = true
		params := mount.LoopParams{
			Readonly:  true,
			Autoclear: true,
		}
		// set up all loop devices
		loop, err := mount.SetupLoop(m.Source, params)
		if err != nil {
			return mount.ActiveMount{}, err
		}
		m.Source = loop.Name()
		loops = append(loops, loop)
		defer func() {
			for _, loop := range loops {
				loop.Close()
			}
		}()

		for i, v := range m.Options {
			// Convert raw files in `device=` into loop devices too
			if strings.HasPrefix(v, "device=") {
				loop, err := mount.SetupLoop(strings.TrimPrefix(v, "device="), params)
				if err != nil {
					return mount.ActiveMount{}, err
				}
				m.Options[i] = "device=" + loop.Name()
				loops = append(loops, loop)
			}
		}
		err = m.Mount(mp)
		if err != nil {
			return mount.ActiveMount{}, err
		}
	} else if err != nil {
		return mount.ActiveMount{}, err
	}

	t := time.Now()
	return mount.ActiveMount{
		Mount:      m,
		MountedAt:  &t,
		MountPoint: mp,
	}, nil
}

// Unmount unmounts the filesystem at the specified path.
func (h *ErofsMountHandler) Unmount(ctx context.Context, path string) error {
	return mount.Unmount(path, 0)
}
