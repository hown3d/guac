//
// Copyright * The GUAC Authors.
//
// Licensed under the Apache License, Version 2.0 (the \"License\");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an \"AS IS\" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package oci

import (
	"context"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/guacsec/guac/pkg/handler/processor"
	"github.com/guacsec/guac/pkg/logging"
	"golang.org/x/sync/errgroup"
)

type Collector interface {
	Collect(context.Context, name.Reference, chan<- *processor.Document, ...remote.Option) error
	Type() string
}

func pushBlobData(ref name.Reference, blobData []byte, artifactType string, docChannel chan<- *processor.Document) {
	docType := processor.DocumentUnknown
	docFormat := processor.FormatUnknown

	if wellKnownArtifactType, ok := wellKnownOCIArtifactTypes[artifactType]; ok {
		docType = wellKnownArtifactType.documentType
		docFormat = wellKnownArtifactType.formatType
	}

	doc := &processor.Document{
		Blob:   blobData,
		Type:   docType,
		Format: docFormat,
		SourceInformation: processor.SourceInformation{
			Collector: OCICollector,
			Source:    ref.String(),
		},
	}
	docChannel <- doc
}

func collect(ctx context.Context, ref name.Reference, docChannel chan<- *processor.Document, remoteOpts ...remote.Option) error {
	g, ctx := errgroup.WithContext(ctx)
	for collectorName, collector := range collectors {
		collector := collector
		collectorName := collectorName
		g.Go(func() error {
			logger := logging.FromContext(ctx)
			logger.Infof("collecting artifacts from %s", collectorName)

			return collector.Collect(ctx, ref, docChannel, remoteOpts...)
		})
	}
	return g.Wait()
}

func collectLayersOfImage(ref name.Reference, img v1.Image, docChannel chan<- *processor.Document) error {
	manifest, err := img.Manifest()
	if err != nil {
		return err
	}

	layers, err := img.Layers()
	if err != nil {
		return err
	}
	for _, layer := range layers {
		blob, err := layer.Compressed()
		if err != nil {
			return err
		}
		defer blob.Close()
		blobData, err := io.ReadAll(blob)
		if err != nil {
			return fmt.Errorf("failed reading blob: %w", err)
		}
		artifactType := "unknown"
		// referrers store their artifactType inside the manifest mediatype
		// https://github.com/opencontainers/image-spec/blob/main/manifest.md#guidelines-for-artifact-usage
		if mediaType := manifest.Config.MediaType; mediaType != "" {
			artifactType = string(mediaType)
		}
		// attestations and sbom store their artifactType in the layer mediatype
		mediaType, err := layer.MediaType()
		if err == nil && mediaType != "" {
			artifactType = string(mediaType)
		}
		pushBlobData(ref, blobData, artifactType, docChannel)
	}
	return nil
}
