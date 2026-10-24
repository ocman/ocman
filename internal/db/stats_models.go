package db

// RecentModel is one model picker's recent-use signal.
type RecentModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	LastUsed int64  `json:"lastUsed"`
}
