package playback

import "anime-shelf/internal/catalog"

// Only verified alternate titles belong here; loose title matching can mix
// remakes, seasons, and live-action adaptations.
func sourceAliases(a catalog.Anime) []string {
	var verified []string
	switch a.ID {
	case 839:
		verified = []string{"未麻的部屋"}
	case 426123:
		verified = []string{"蓝色监狱 第二季", "蓝色禁区 第二季"}
	}
	seen := map[string]bool{norm(a.Title): true, norm(a.Native): true}
	var result []string
	for _, alias := range append(verified, a.Aliases...) {
		key := norm(alias)
		if len([]rune(key)) < 4 || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, alias)
	}
	return result
}

func searchTitles(a catalog.Anime) []string {
	titles := []string{a.Title}
	for _, alias := range sourceAliases(a) {
		if len(titles) >= 5 {
			break
		}
		titles = append(titles, alias)
	}
	return titles
}
