package searchindex

import "sort"

func CandidateFileIndexes(artifact *SliceArtifact, query *QueryNode) []uint32 {
	if artifact == nil {
		return nil
	}
	fileCount := len(artifact.Files)
	if fileCount == 0 {
		return nil
	}
	postings := make(map[string][]uint32, len(artifact.Postings))
	for _, posting := range artifact.Postings {
		if posting.NGram == "" {
			continue
		}
		postings[posting.NGram] = append([]uint32(nil), posting.FileIndexes...)
	}
	indexes := evaluateArtifactQuery(query, postings, fileCount)
	if len(indexes) == 0 {
		return nil
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	return indexes
}

func evaluateArtifactQuery(node *QueryNode, postings map[string][]uint32, fileCount int) []uint32 {
	if node == nil || fileCount == 0 {
		return allArtifactFileIndexes(fileCount)
	}

	switch node.Kind {
	case QueryNodeTrue:
		return allArtifactFileIndexes(fileCount)
	case QueryNodeTerm:
		if len(node.NGrams) == 0 {
			return allArtifactFileIndexes(fileCount)
		}
		var current map[uint32]struct{}
		for _, gram := range node.NGrams {
			indexes, ok := postings[gram]
			if !ok {
				return nil
			}
			if current == nil {
				current = make(map[uint32]struct{}, len(indexes))
				for _, index := range indexes {
					current[index] = struct{}{}
				}
				continue
			}
			next := make(map[uint32]struct{})
			for _, index := range indexes {
				if _, ok := current[index]; ok {
					next[index] = struct{}{}
				}
			}
			current = next
			if len(current) == 0 {
				return nil
			}
		}
		return sortedArtifactIndexSet(current)
	case QueryNodeAnd:
		var current map[uint32]struct{}
		for _, child := range node.Children {
			childIndexes := evaluateArtifactQuery(child, postings, fileCount)
			if len(childIndexes) == 0 {
				return nil
			}
			if current == nil {
				current = make(map[uint32]struct{}, len(childIndexes))
				for _, index := range childIndexes {
					current[index] = struct{}{}
				}
				continue
			}
			next := make(map[uint32]struct{})
			for _, index := range childIndexes {
				if _, ok := current[index]; ok {
					next[index] = struct{}{}
				}
			}
			current = next
			if len(current) == 0 {
				return nil
			}
		}
		return sortedArtifactIndexSet(current)
	case QueryNodeOr:
		out := make(map[uint32]struct{})
		for _, child := range node.Children {
			for _, index := range evaluateArtifactQuery(child, postings, fileCount) {
				out[index] = struct{}{}
			}
		}
		return sortedArtifactIndexSet(out)
	default:
		return allArtifactFileIndexes(fileCount)
	}
}

func allArtifactFileIndexes(fileCount int) []uint32 {
	indexes := make([]uint32, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		indexes = append(indexes, uint32(i))
	}
	return indexes
}

func sortedArtifactIndexSet(values map[uint32]struct{}) []uint32 {
	if len(values) == 0 {
		return nil
	}
	indexes := make([]uint32, 0, len(values))
	for value := range values {
		indexes = append(indexes, value)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	return indexes
}
