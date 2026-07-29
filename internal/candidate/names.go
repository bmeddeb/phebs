package candidate

import "github.com/bmeddeb/phebs/internal/candidateid"

func ArtifactBase(repository string) string {
	return candidateid.ArtifactBase(repository)
}

func ManifestName(repository string) string {
	return candidateid.ManifestName(repository)
}

func PublishingName(repository string) string {
	return candidateid.PublishingName(repository)
}

func ArtifactPrefix(repository, generationDigest string) string {
	return candidateid.ArtifactPrefix(repository, generationDigest)
}
