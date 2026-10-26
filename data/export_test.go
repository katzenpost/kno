package knorepo

import "io/fs"

func Networks() ([]string, error) {
	ents, err := fs.ReadDir(files, "networks")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
