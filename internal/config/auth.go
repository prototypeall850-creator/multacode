package config

// Auth stores provider credentials separately from Config.
// Never write Auth contents into memory files or sessions.
type Auth map[string]string // provider id -> api key

func LoadAuth(path string) (Auth, error) {
	var a Auth
	if err := loadJSON(path, &a); err != nil {
		return nil, err
	}
	if a == nil {
		a = Auth{}
	}
	return a, nil
}

func SaveAuth(path string, a Auth) error {
	return saveJSON(path, a)
}
