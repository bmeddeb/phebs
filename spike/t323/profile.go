package t323

import (
	"fmt"
	"slices"
)

func GenerateLoadProfile(serviceCount int) (LoadProfile, error) {
	if serviceCount != 1_000 && serviceCount != 5_000 {
		return LoadProfile{}, fmt.Errorf("service count %d is not a frozen profile", serviceCount)
	}
	contents, err := GenerateLoadProfileContents(serviceCount)
	if err != nil {
		return LoadProfile{}, err
	}
	profile := LoadProfile{
		Schema: LoadProfileSchema,
		Name:   fmt.Sprintf("services-%d", serviceCount),
		Seed:   "t323-neutral-load-v1",
		Claims: ProfileClaims{Synthetic: true},
	}
	for index := 0; index < serviceCount; index++ {
		serviceKey := fmt.Sprintf("svc.load-%05d", index)
		primaryDirectory := fmt.Sprintf("services/service-%05d", index)
		generatedPath := fmt.Sprintf("gen/service-%05d/client.pb.go", index)
		typedPath := fmt.Sprintf("typed/service-%05d/index.scip", index)
		sharedPath := fmt.Sprintf("shared/group-%04d/library.go", index/10)
		contractPath := fmt.Sprintf("contracts/group-%04d.proto", index/25)
		profile.Services = append(profile.Services, ProfileService{
			ServiceKey: serviceKey,
			Placements: []Placement{
				placement(primaryDirectory, "primary"),
				placement(contractPath, "supporting"),
				placement(sharedPath, "shared"),
				placement(generatedPath, "generated"),
				placement(typedPath, "typed"),
			},
		})
	}

	paths := make([]string, 0, len(contents))
	for filePath := range contents {
		paths = append(paths, filePath)
	}
	slices.Sort(paths)
	contentDigests := map[string]struct{}{}
	var contentBytes int64
	for _, filePath := range paths {
		content := contents[filePath]
		file := ProfileFile{Path: filePath, Bytes: len(content), SHA256: SHA256(content)}
		profile.Files = append(profile.Files, file)
		contentDigests[file.SHA256] = struct{}{}
		contentBytes += int64(file.Bytes)
	}
	profile.Cardinality = ProfileCardinality{
		Services: serviceCount, Files: len(profile.Files),
		DistinctContents: len(contentDigests), ContentBytes: contentBytes,
		Memberships: serviceCount * 5,
		RoleMemberships: map[string]int{
			"primary": serviceCount, "supporting": serviceCount,
			"shared": serviceCount, "generated": serviceCount, "typed": serviceCount,
		},
		MaxPathFanout: 25, SharedFiles: ceilingDivision(serviceCount, 10),
		ContractFiles: ceilingDivision(serviceCount, 25), UnownedFiles: ceilingDivision(serviceCount, 100),
	}
	if err := ValidateProfile(profile); err != nil {
		return LoadProfile{}, err
	}
	return profile, nil
}

// GenerateLoadProfileContents reproduces the exact public file bytes committed
// by a frozen load profile. It lets later spike tickets measure the retained
// profile without treating its inventory as a production corpus contract.
func GenerateLoadProfileContents(serviceCount int) (map[string][]byte, error) {
	if serviceCount != 1_000 && serviceCount != 5_000 {
		return nil, fmt.Errorf("service count %d is not a frozen profile", serviceCount)
	}
	files := map[string][]byte{
		"go.mod": []byte("module example.invalid/t323-load\n\ngo 1.26\n"),
	}
	for index := 0; index < serviceCount; index++ {
		files[fmt.Sprintf("services/service-%05d/main.go", index)] = []byte(fmt.Sprintf(
			"package service%05d\n\nconst Needle = \"T323_LOAD_SERVICE_%05d\"\n", index, index,
		))
		files[fmt.Sprintf("gen/service-%05d/client.pb.go", index)] = []byte(fmt.Sprintf(
			"// Code generated. DO NOT EDIT.\npackage service%05d\n\nconst Generated = \"T323_LOAD_GENERATED_%05d\"\n", index, index,
		))
		files[fmt.Sprintf("typed/service-%05d/index.scip", index)] = []byte(fmt.Sprintf(
			"t323-synthetic-typed-input-v1\x00%05d\n", index,
		))
	}
	for index := 0; index < ceilingDivision(serviceCount, 10); index++ {
		files[fmt.Sprintf("shared/group-%04d/library.go", index)] = []byte(fmt.Sprintf(
			"package shared%04d\n\nconst Needle = \"T323_LOAD_SHARED_%04d\"\n", index, index,
		))
	}
	for index := 0; index < ceilingDivision(serviceCount, 25); index++ {
		files[fmt.Sprintf("contracts/group-%04d.proto", index)] = []byte(fmt.Sprintf(
			"syntax = \"proto3\";\npackage neutral.load.group%04d;\n// T323_LOAD_CONTRACT_%04d\n", index, index,
		))
	}
	for index := 0; index < ceilingDivision(serviceCount, 100); index++ {
		files[fmt.Sprintf("tools/unowned-%04d.go", index)] = []byte(fmt.Sprintf(
			"package tools\n\nconst Unowned%04d = \"T323_LOAD_UNOWNED_%04d\"\n", index, index,
		))
	}
	return files, nil
}

func BuildArtifacts() (map[string][]byte, error) {
	snapshots, err := SmallSnapshots()
	if err != nil {
		return nil, err
	}
	artifacts := make(map[string][]byte, len(snapshots)*2+2)
	for _, snapshot := range snapshots {
		catalogBytes, err := MarshalCanonical(snapshot.Catalog)
		if err != nil {
			return nil, err
		}
		oracleBytes, err := MarshalCanonical(snapshot.Oracle)
		if err != nil {
			return nil, err
		}
		artifacts["catalogs/"+snapshot.Revision+".json"] = catalogBytes
		artifacts["oracles/"+snapshot.Revision+".json"] = oracleBytes
	}
	for _, count := range []int{1_000, 5_000} {
		profile, err := GenerateLoadProfile(count)
		if err != nil {
			return nil, err
		}
		profileBytes, err := MarshalCanonical(profile)
		if err != nil {
			return nil, err
		}
		artifacts[fmt.Sprintf("profiles/services-%d.json", count)] = profileBytes
	}
	return artifacts, nil
}

func ceilingDivision(value, divisor int) int {
	return (value + divisor - 1) / divisor
}
