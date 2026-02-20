package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const cacheTTL = 24 * time.Hour

type Member struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

// apiMember represents the relevant fields from the GitLab API response.
type apiMember struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	State    string `json:"state"`
}

// gitlabProject holds the parsed host and project path from a git remote URL.
type gitlabProject struct {
	Host string // e.g. "gitlab.com"
	Path string // e.g. "researchable/myproject"
}

func main() {
	refresh := flag.Bool("refresh", false, "Force refresh the cache from GitLab API")
	jsonOut := flag.Bool("json", false, "Output as JSON instead of TSV")
	flag.Parse()

	members, err := getMembers(*refresh)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(members); err != nil {
			fmt.Fprintf(os.Stderr, "error encoding json: %v\n", err)
			os.Exit(1)
		}
	} else {
		for _, m := range members {
			fmt.Printf("%s\t%s\n", m.Name, m.Username)
		}
	}
}

func getMembers(forceRefresh bool) ([]Member, error) {
	cachePath, cachePathErr := getCachePath()

	// Try to use cache if not forcing refresh and cache path is available
	if cachePathErr == nil && !forceRefresh {
		members, err := readCache(cachePath)
		if err == nil {
			return members, nil
		}
		// Cache miss or stale, continue to refresh
	}

	// Try GitLab API directly
	members, err := fetchFromGitLab()
	if err == nil {
		// Write cache (best effort)
		if cachePathErr == nil {
			if writeErr := writeCache(cachePath, members); writeErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not write cache: %v\n", writeErr)
			}
		}
		return members, nil
	}

	fmt.Fprintf(os.Stderr, "warning: GitLab API failed: %v\n", err)

	// Try stale cache
	if cachePathErr == nil {
		members, staleErr := readCacheIgnoreTTL(cachePath)
		if staleErr == nil {
			fmt.Fprintf(os.Stderr, "warning: using stale cache\n")
			return members, nil
		}
	}

	// Last resort: git log
	fmt.Fprintf(os.Stderr, "warning: falling back to git log contributors (no GitLab usernames available)\n")
	members, gitLogErr := fetchFromGitLog()
	if gitLogErr != nil {
		fmt.Fprintf(os.Stderr, "warning: git log failed: %v\n", gitLogErr)
		return []Member{}, nil
	}

	return members, nil
}

func getRemoteURL() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("not a git repo or no origin remote: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func getCachePath() (string, error) {
	remoteURL, err := getRemoteURL()
	if err != nil {
		return "", err
	}

	project, err := parseGitLabRemote(remoteURL)
	if err != nil {
		// Non-GitLab remote: use a sanitized version of the URL
		sanitized := strings.NewReplacer("/", "-", ":", "-", "@", "-", ".", "-").Replace(remoteURL)
		project = &gitlabProject{Path: sanitized}
	}

	// Turn "researchable/general/my-project" into "researchable-general-my-project"
	filename := strings.ReplaceAll(project.Path, "/", "-") + ".json"

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), ".cache")
	}

	return filepath.Join(cacheDir, "gitlab-reviewer", filename), nil
}

// parseGitLabRemote extracts the host and project path from a git remote URL.
// Supports both SSH and HTTPS formats:
//
//	git@gitlab.com:group/project.git
//	https://gitlab.com/group/project.git
func parseGitLabRemote(remoteURL string) (*gitlabProject, error) {
	// Try SSH format: git@host:path.git
	sshRe := regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)
	if m := sshRe.FindStringSubmatch(remoteURL); m != nil {
		return &gitlabProject{Host: m[1], Path: m[2]}, nil
	}

	// Try HTTPS format: https://host/path.git
	u, err := url.Parse(remoteURL)
	if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		if path != "" {
			return &gitlabProject{Host: u.Host, Path: path}, nil
		}
	}

	return nil, fmt.Errorf("could not parse remote URL: %s", remoteURL)
}

// readPAT reads the GitLab personal access token from ~/.gitlab_pat.
func readPAT() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".gitlab_pat"))
	if err != nil {
		return "", fmt.Errorf("could not read ~/.gitlab_pat: %w", err)
	}

	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("~/.gitlab_pat is empty")
	}

	return token, nil
}

func readCache(path string) ([]Member, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if time.Since(info.ModTime()) > cacheTTL {
		return nil, fmt.Errorf("cache is stale")
	}

	return readCacheIgnoreTTL(path)
}

func readCacheIgnoreTTL(path string) ([]Member, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var members []Member
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, fmt.Errorf("parsing cache: %w", err)
	}

	if len(members) == 0 {
		return nil, fmt.Errorf("cache is empty")
	}

	return members, nil
}

func writeCache(path string, members []Member) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(members, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

func fetchFromGitLab() ([]Member, error) {
	remoteURL, err := getRemoteURL()
	if err != nil {
		return nil, err
	}

	project, err := parseGitLabRemote(remoteURL)
	if err != nil {
		return nil, err
	}

	token, err := readPAT()
	if err != nil {
		return nil, err
	}

	// URL-encode the project path for the API call
	encodedPath := url.PathEscape(project.Path)
	apiURL := fmt.Sprintf("https://%s/api/v4/projects/%s/members/all?per_page=100", project.Host, encodedPath)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Truncate body to avoid dumping entire HTML error pages
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, preview)
	}

	var apiMembers []apiMember
	if err := json.Unmarshal(body, &apiMembers); err != nil {
		return nil, fmt.Errorf("parsing API response: %w", err)
	}

	var members []Member
	for _, am := range apiMembers {
		if am.State != "active" {
			continue
		}
		members = append(members, Member{
			Name:     am.Name,
			Username: am.Username,
		})
	}

	if len(members) == 0 {
		return nil, fmt.Errorf("no active members found")
	}

	return members, nil
}

func fetchFromGitLog() ([]Member, error) {
	out, err := exec.Command("git", "log", "--format=%aN").Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	// Deduplicate names
	seen := make(map[string]bool)
	var members []Member

	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		members = append(members, Member{
			Name:     name,
			Username: "", // Unknown without GitLab API
		})
	}

	return members, nil
}
