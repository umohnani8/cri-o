package server

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"

	"github.com/containers/storage"
	"github.com/cri-o/cri-o/internal/log"
	pkgstorage "github.com/cri-o/cri-o/internal/storage"
	"github.com/cri-o/cri-o/server/cri/types"
	json "github.com/json-iterator/go"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// ImageStatus returns the status of the image.
func (s *Server) ImageStatus(ctx context.Context, req *types.ImageStatusRequest) (*types.ImageStatusResponse, error) {
	c1 := make(chan *types.ImageStatusResponse)
	c2 := make(chan error)
	go func() {
		resp, err := s.imageStatus(ctx, req)
		c1 <- resp
		c2 <- err
	}()

	file, err := os.Create("/home/umohnani/go/src/github.com/cri-o/cri-o/pprof.txt")
	if err != nil {
		return nil, errors.Wrapf(err, "error creating file")
	}

	prof := pprof.Lookup("goroutine")
	prof.WriteTo(file, 2)
	fmt.Println("---count---:", prof.Count())
	fmt.Println("---name----:", prof.Name())
	return <-c1, <-c2
}

// ImageStatus returns the status of the image.
func (s *Server) imageStatus(ctx context.Context, req *types.ImageStatusRequest) (*types.ImageStatusResponse, error) {
	var resp *types.ImageStatusResponse
	image := ""
	img := req.Image
	if img != nil {
		image = img.Image
	}
	if image == "" {
		return nil, fmt.Errorf("no image specified")
	}

	log.Infof(ctx, "Checking image status: %s", image)
	images, err := s.StorageImageServer().ResolveNames(s.config.SystemContext, image)
	if err != nil {
		if err == pkgstorage.ErrCannotParseImageID {
			images = append(images, image)
		} else {
			return nil, err
		}
	}
	var (
		notfound bool
		lastErr  error
	)
	for _, image := range images {
		status, err := s.StorageImageServer().ImageStatus(s.config.SystemContext, image)
		if err != nil {
			if errors.Cause(err) == storage.ErrImageUnknown {
				log.Debugf(ctx, "can't find %s", image)
				notfound = true
				continue
			}
			log.Warnf(ctx, "error getting status from %s: %v", image, err)
			lastErr = err
			continue
		}

		// Ensure that size is already defined
		var size uint64
		if status.Size == nil {
			size = 0
		} else {
			size = *status.Size
		}

		resp = &types.ImageStatusResponse{
			Image: &types.Image{
				ID:          status.ID,
				RepoTags:    status.RepoTags,
				RepoDigests: status.RepoDigests,
				Size:        size,
			},
		}
		if req.Verbose {
			info, err := createImageInfo(status)
			if err != nil {
				return nil, errors.Wrap(err, "creating image info")
			}
			resp.Info = info
		}
		uid, username := getUserFromImage(status.User)
		if uid != nil {
			resp.Image.UID = &types.Int64Value{Value: *uid}
		}
		resp.Image.Username = username
		break
	}
	if lastErr != nil && resp == nil {
		return nil, lastErr
	}
	if notfound && resp == nil {
		log.Infof(ctx, "Image %s not found", image)
		return &types.ImageStatusResponse{}, nil
	}

	log.Infof(ctx, "Image status: %v", resp)
	return resp, nil
}

// getUserFromImage gets uid or user name of the image user.
// If user is numeric, it will be treated as uid; or else, it is treated as user name.
func getUserFromImage(user string) (id *int64, username string) {
	// return both empty if user is not specified in the image.
	if user == "" {
		return nil, ""
	}
	// split instances where the id may contain user:group
	user = strings.Split(user, ":")[0]
	// user could be either uid or user name. Try to interpret as numeric uid.
	uid, err := strconv.ParseInt(user, 10, 64)
	if err != nil {
		// If user is non numeric, assume it's user name.
		return nil, user
	}
	// If user is a numeric uid.
	return &uid, ""
}

func createImageInfo(result *pkgstorage.ImageResult) (map[string]string, error) {
	info := struct {
		Labels    map[string]string `json:"labels,omitempty"`
		ImageSpec *specs.Image      `json:"imageSpec"`
	}{
		result.Labels,
		result.OCIConfig,
	}
	bytes, err := json.Marshal(info)
	if err != nil {
		return nil, errors.Wrapf(err, "marshal data: %v", info)
	}
	return map[string]string{"info": string(bytes)}, nil
}
