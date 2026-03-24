package searchindex

import "sort"

type SparseNGram struct {
	Value  string
	Start  int
	End    int
	Weight uint32
}

func BuildSparseNGrams(content []byte, weighter BigramWeighter, mode SparseMode) []SparseNGram {
	if len(content) < 2 {
		return nil
	}
	if weighter == nil {
		weighter = DefaultWeighter()
	}
	all := buildAllSparseNGrams(content, weighter)
	switch mode {
	case SparseModeCovering:
		return buildCoveringSparseNGrams(all, len(content))
	default:
		return all
	}
}

func UniqueNGramValues(grams []SparseNGram) []string {
	if len(grams) == 0 {
		return nil
	}
	values := make([]string, 0, len(grams))
	for _, gram := range grams {
		if gram.Value == "" {
			continue
		}
		values = append(values, gram.Value)
	}
	return uniqueSortedStrings(values)
}

func buildAllSparseNGrams(content []byte, weighter BigramWeighter) []SparseNGram {
	weights := make([]uint32, len(content)-1)
	for i := 0; i < len(content)-1; i++ {
		weights[i] = weighter.WeightPair(content[i], content[i+1])
	}

	grams := make([]SparseNGram, 0, len(weights)*2)
	for start := 0; start < len(weights); start++ {
		var maxInterior uint32
		for endPair := start; endPair < len(weights); endPair++ {
			if endPair-start >= 2 {
				interiorWeight := weights[endPair-1]
				if interiorWeight > maxInterior {
					maxInterior = interiorWeight
				}
			}
			edgeMin := weights[start]
			if weights[endPair] < edgeMin {
				edgeMin = weights[endPair]
			}
			if endPair == start || edgeMin > maxInterior {
				grams = append(grams, SparseNGram{
					Value:  string(content[start : endPair+2]),
					Start:  start,
					End:    endPair + 2,
					Weight: edgeMin,
				})
			}
		}
	}

	sort.Slice(grams, func(i, j int) bool {
		if grams[i].Start != grams[j].Start {
			return grams[i].Start < grams[j].Start
		}
		if grams[i].End != grams[j].End {
			return grams[i].End > grams[j].End
		}
		if grams[i].Weight != grams[j].Weight {
			return grams[i].Weight > grams[j].Weight
		}
		return grams[i].Value < grams[j].Value
	})
	return grams
}

func buildCoveringSparseNGrams(all []SparseNGram, contentLen int) []SparseNGram {
	if len(all) == 0 {
		return nil
	}

	byStart := make(map[int][]SparseNGram)
	for _, gram := range all {
		byStart[gram.Start] = append(byStart[gram.Start], gram)
	}
	for start := range byStart {
		sort.Slice(byStart[start], func(i, j int) bool {
			if byStart[start][i].End != byStart[start][j].End {
				return byStart[start][i].End > byStart[start][j].End
			}
			if byStart[start][i].Weight != byStart[start][j].Weight {
				return byStart[start][i].Weight > byStart[start][j].Weight
			}
			return byStart[start][i].Value < byStart[start][j].Value
		})
	}

	covered := make([]SparseNGram, 0, len(all)/2+1)
	current := 0
	for current < contentLen-1 {
		candidates := byStart[current]
		if len(candidates) == 0 {
			current++
			continue
		}
		chosen := candidates[0]
		covered = append(covered, chosen)
		next := chosen.End - 1
		if next <= current {
			current++
			continue
		}
		current = next
	}
	return covered
}
