package planner

import "regexp"

var tmdbPattern = regexp.MustCompile(`\{tmdb-(\d+)\}`)

// ParseTMDBID extracts the id that Radarr/Sonarr already put in the folder
// name, e.g. "/media/movies/Babylon (2022) {tmdb-615777}/Babylon.mkv".
// Parsing it here saves an API round trip and works offline.
func ParseTMDBID(path string) *int64 {
	m := tmdbPattern.FindStringSubmatch(path)
	if len(m) != 2 {
		return nil
	}
	var id int64
	for _, r := range m[1] {
		id = id*10 + int64(r-'0')
	}
	if id <= 0 {
		return nil
	}
	return &id
}
