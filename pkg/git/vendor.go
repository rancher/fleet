package git

import (
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
)

// getVendorCommitsURL returns the commits url for supported
// git repositories.
// In case or using a non supported git repository or in case of error it
// returns an empty string.
// Supported git repositories are: github and git.rancher.io
func getVendorCommitsURL(url, branch string) string {
	u, err := neturl.Parse(url)
	if err != nil {
		return ""
	}
	switch u.Hostname() {
	case "github.com":
		return getGithubCommitsURL(u, branch)
	case "git.rancher.io":
		return getRancherCommitsURL(u, branch)
	}

	return ""
}

// getGithubCommitsURL returns the commits url for github
// Returns an empty string in case of error
func getGithubCommitsURL(url *neturl.URL, branch string) string {
	pathParts := strings.Split(url.Path, "/")
	if len(pathParts) >= 3 {
		org := pathParts[1]
		repo := strings.TrimSuffix(pathParts[2], ".git")
		return fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", org, repo, branch)
	}

	return ""
}

// getRancherCommitsURL returns the commits url for git.rancher.io
// Returns an empty string in case of error
func getRancherCommitsURL(url *neturl.URL, branch string) string {
	pathParts := strings.Split(url.Path, "/")
	if len(pathParts) > 1 {
		repo := strings.TrimSuffix(pathParts[1], ".git")
		url.Path = fmt.Sprintf("/repos/%s/commits/%s", repo, branch)
		return url.String()
	}

	return ""
}

// latestCommitFromCommitsURL returns the latest commit using the given commits url
func latestCommitFromCommitsURL(commitsUrl string, opts *options) (string, error) {
	client, err := GetHTTPClientFromSecret(
		opts.Credential,
		opts.CABundle,
		opts.InsecureTLSVerify,
		opts.Timeout,
	)
	if err != nil {
		return "", err
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequest("GET", commitsUrl, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/vnd.github.v3.sha")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("could not get latest commit from %s. Returned error code: %d", commitsUrl, resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	bodyString := string(bodyBytes)
	return bodyString, nil
}
