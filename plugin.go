package music

type Plugin interface {
	Search(string) ([]string, error)
	Download(string) (string, error)
}