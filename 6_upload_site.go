package nevsin

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var UploadSiteCmd = &cobra.Command{
	Use:   "upload-site",
	Short: "Upload HTML report to GitHub Pages",
	Run: func(cmd *cobra.Command, args []string) {
		// Check if report.html exists
		if _, err := os.Stat("report.html"); os.IsNotExist(err) {
			log.Fatalf("report.html not found. Run 'generate-html' first.")
		}

		// Check if report.md exists
		if _, err := os.Stat("report.md"); os.IsNotExist(err) {
			log.Fatalf("report.md not found. Run 'generate-report' first.")
		}

		// Create or update GitHub Pages repository
		if err := uploadToGitHubPages(); err != nil {
			log.Fatalf("Failed to upload to GitHub Pages: %v", err)
		}

		log.Println("Successfully uploaded to GitHub Pages")
	},
}

// uploadToGitHubPages handles the GitHub Pages upload process
func uploadToGitHubPages() error {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %v", err)
	}

	// Create a temporary directory for the gh-pages branch
	tempDir := filepath.Join(cwd, "gh-pages-temp")
	if err := os.RemoveAll(tempDir); err != nil {
		return fmt.Errorf("failed to remove existing temp directory: %v", err)
	}

	// Check if we're in a git repository
	if _, err := exec.Command("git", "rev-parse", "--git-dir").Output(); err != nil {
		return fmt.Errorf("not in a git repository")
	}

	// Get the remote repository URL
	remoteURL, err := exec.Command("git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return fmt.Errorf("failed to get remote URL: %v", err)
	}
	remoteURLStr := strings.TrimSpace(string(remoteURL))

	// Clone the repository to temp directory
	log.Printf("Cloning repository for GitHub Pages...")
	if err := exec.Command("git", "clone", remoteURLStr, tempDir).Run(); err != nil {
		return fmt.Errorf("failed to clone repository: %v", err)
	}

	// Change to temp directory
	if err := os.Chdir(tempDir); err != nil {
		return fmt.Errorf("failed to change to temp directory: %v", err)
	}

	// Check if gh-pages branch exists remotely
	_, err = exec.Command("git", "show-ref", "--verify", "--quiet", "refs/remotes/origin/gh-pages").Output()
	ghPagesBranchExistsRemotely := err == nil

	if ghPagesBranchExistsRemotely {
		// Switch to gh-pages branch (create local branch if it doesn't exist)
		if err := exec.Command("git", "checkout", "gh-pages").Run(); err != nil {
			// If local branch doesn't exist, create it from remote
			if err := exec.Command("git", "checkout", "-b", "gh-pages", "origin/gh-pages").Run(); err != nil {
				return fmt.Errorf("failed to checkout gh-pages branch: %v", err)
			}
		}
	} else {
		// Create orphan gh-pages branch
		if err := exec.Command("git", "checkout", "--orphan", "gh-pages").Run(); err != nil {
			return fmt.Errorf("failed to create gh-pages branch: %v", err)
		}

		// Remove all files from the orphan branch
		if err := exec.Command("git", "rm", "-rf", ".").Run(); err != nil {
			// This might fail if there are no files, which is okay
			log.Printf("Warning: failed to remove files from orphan branch: %v", err)
		}
	}

	// Copy the HTML file to the temp directory
	htmlSource := filepath.Join(cwd, "report.html")
	htmlDest := filepath.Join(tempDir, "index.html")

	htmlData, err := os.ReadFile(htmlSource)
	if err != nil {
		return fmt.Errorf("failed to read report.html: %v", err)
	}

	if err := os.WriteFile(htmlDest, htmlData, 0644); err != nil {
		return fmt.Errorf("failed to write index.html: %v", err)
	}

	// Copy the markdown file to the temp directory
	mdSource := filepath.Join(cwd, "report.md")
	mdDest := filepath.Join(tempDir, "index.md")

	mdData, err := os.ReadFile(mdSource)
	if err != nil {
		return fmt.Errorf("failed to read report.md: %v", err)
	}

	if err := os.WriteFile(mdDest, mdData, 0644); err != nil {
		return fmt.Errorf("failed to write index.md: %v", err)
	}

	// Add the files to git
	if err := exec.Command("git", "add", "index.html", "index.md").Run(); err != nil {
		return fmt.Errorf("failed to add files to git: %v", err)
	}

	// Check if there are changes to commit
	statusOutput, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("failed to check git status: %v", err)
	}

	if len(strings.TrimSpace(string(statusOutput))) == 0 {
		log.Println("No changes to commit")
		return nil
	}

	// Commit the changes
	commitMessage := fmt.Sprintf("Update news report - %s", time.Now().Format("2006-01-02 15:04:05"))
	if err := exec.Command("git", "commit", "-m", commitMessage).Run(); err != nil {
		return fmt.Errorf("failed to commit changes: %v", err)
	}

	// Push to gh-pages branch
	pushCmd := exec.Command("git", "push", "origin", "gh-pages")
	pushOutput, err := pushCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to push to gh-pages branch: %v\nOutput: %s", err, string(pushOutput))
	}

	// Change back to original directory
	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("failed to change back to original directory: %v", err)
	}

	// Clean up temp directory
	if err := os.RemoveAll(tempDir); err != nil {
		log.Printf("Warning: failed to remove temp directory: %v", err)
	}

	return nil
}