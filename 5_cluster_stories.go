package nevsin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
	"gonum.org/v1/gonum/mat"
)

// StoryCluster represents a cluster of similar stories
type StoryCluster struct {
	ClusterID int              `json:"cluster_id"`
	Stories   []ClusteredStory `json:"stories"`
	Centroid  []float64        `json:"-"` // Don't serialize centroid
}

// ClusteredStory represents a story within a cluster
type ClusteredStory struct {
	StoryID    string  `json:"story_id"`
	VideoID    string  `json:"video_id"`
	Title      string  `json:"title"`
	Summary    string  `json:"summary"`
	Reporter   string  `json:"reporter"`
	Similarity float64 `json:"similarity_to_centroid"`
}

// ClusteringResult represents the output of clustering
type ClusteringResult struct {
	Clusters []StoryCluster `json:"clusters"`
	Summary  ClusterSummary `json:"summary"`
}

// ClusterSummary provides metadata about clustering results
type ClusterSummary struct {
	TotalStories          int            `json:"total_stories"`
	TotalClusters         int            `json:"total_clusters"`
	AverageSilhouette     float64        `json:"average_silhouette_score"`
	DaviesBouldinIndex    float64        `json:"davies_bouldin_index"`
	IntraClusterDistance  float64        `json:"avg_intra_cluster_distance"`
	InterClusterDistance  float64        `json:"avg_inter_cluster_distance"`
	ClusterSizes          []int          `json:"cluster_sizes"`
	ReporterDistribution  map[string]int `json:"reporter_distribution"`
	CrossReporterClusters int            `json:"cross_reporter_clusters"`
	ClusterImportance     []float64      `json:"cluster_importance_scores"`
	QualityAssessment     string         `json:"quality_assessment"`
}

// KEvaluationResult holds the results of evaluating clustering with a specific K value
type KEvaluationResult struct {
	K                     int     `json:"k"`
	SilhouetteScore       float64 `json:"silhouette_score"`
	DaviesBouldinIndex    float64 `json:"davies_bouldin_index"`
	CalinskiHarabaszIndex float64 `json:"calinski_harabasz_index"`
	WCSS                  float64 `json:"wcss"`
	CrossReporterRatio    float64 `json:"cross_reporter_ratio"`
	ClusterCoherence      float64 `json:"cluster_coherence"`
	WeightedScore         float64 `json:"weighted_score"`
	ClusterSizes          []int   `json:"cluster_sizes"`
}

var ClusterStoriesCmd = &cobra.Command{
	Use:   "cluster-stories",
	Short: "Cluster stories using embeddings",
	Run: func(cmd *cobra.Command, args []string) {
		if err := clusterAllStories(); err != nil {
			log.Printf("Failed to cluster stories: %v", err)
			return
		}
		log.Println("Story clustering complete.")
	},
}

// clusterAllStories loads embeddings and performs clustering
func clusterAllStories() error {
	// Open database
	db, err := sql.Open("sqlite3", "embeddings.db")
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("Failed to close database: %v", err)
		}
	}()

	// Load all embeddings
	embeddings, err := loadAllEmbeddings(db)
	if err != nil {
		return fmt.Errorf("failed to load embeddings: %w", err)
	}

	if len(embeddings) == 0 {
		return fmt.Errorf("no embeddings found in database")
	}

	log.Printf("Loaded %d embeddings for clustering", len(embeddings))

	// Normalize embeddings using L2 normalization
	normalizedEmbeddings := normalizeEmbeddings(embeddings)
	log.Printf("🔄 Applied L2 normalization to %d embeddings", len(normalizedEmbeddings))

	// Remove outliers to improve clustering quality
	filteredEmbeddings, removedCount := removeOutliers(normalizedEmbeddings)
	if removedCount > 0 {
		log.Printf("🗑️  Removed %d outlier stories for better grouping quality", removedCount)
		log.Printf("📊 Clustering %d stories after outlier removal", len(filteredEmbeddings))
	}

	// Try DBSCAN first for better natural clustering
	log.Printf("🔍 Attempting DBSCAN clustering for natural cluster discovery...")
	clusters, dbscanSuccess := tryStableDBSCAN(filteredEmbeddings)

	// Calculate how many stories were actually clustered
	storiesInClusters := 0
	for _, cluster := range clusters {
		storiesInClusters += len(cluster.Stories)
	}
	coverageRatio := float64(storiesInClusters) / float64(len(filteredEmbeddings))

	log.Printf("🔧 DBSCAN result: success=%t, clusters=%d, coverage=%.1f%% (%d/%d stories)",
		dbscanSuccess, len(clusters), coverageRatio*100, storiesInClusters, len(filteredEmbeddings))

	// Reject DBSCAN if it leaves too many stories as noise (< 60% coverage)
	if !dbscanSuccess || len(clusters) < 2 || coverageRatio < 0.6 {
		if coverageRatio < 0.6 {
			log.Printf("⚠️  DBSCAN left too many stories as noise (%.1f%% coverage < 60%%), rejecting", coverageRatio*100)
		} else {
			log.Printf("⚠️  DBSCAN failed stability test or found too few clusters")
		}

		// Try hierarchical clustering first - better for non-spherical news story clusters
		log.Printf("🌳 Attempting hierarchical agglomerative clustering...")
		k := findOptimalK(filteredEmbeddings)
		clusters, err = performHierarchicalClustering(filteredEmbeddings, k)

		if err != nil {
			log.Printf("⚠️  Hierarchical clustering failed: %v, falling back to K-means", err)
			// Final fallback to K-means
			clusters, err = performStableKMeansClustering(filteredEmbeddings, k)
		} else {
			log.Printf("✅ Hierarchical clustering created %d clusters", len(clusters))
		}
	} else {
		log.Printf("✅ DBSCAN found %d stable natural clusters", len(clusters))
		err = nil // Clear any previous error
	}
	if err != nil {
		return fmt.Errorf("failed to perform clustering: %w", err)
	}

	// Eliminate single-story clusters by merging them into most similar clusters
	clusters = mergeSingleStoryClusters(filteredEmbeddings, clusters)
	log.Printf("🔗 After merging single-story clusters: %d clusters", len(clusters))

	// Split any low-coherence clusters that are too loosely grouped
	clusters = splitLowCoherenceClusters(filteredEmbeddings, clusters)
	log.Printf("✂️  After splitting low-coherence clusters: %d final clusters", len(clusters))

	// Calculate comprehensive clustering quality metrics on filtered data
	silhouetteScore := calculateSilhouetteScore(filteredEmbeddings, clusters)
	daviesBouldinIndex := calculateDaviesBouldinIndex(filteredEmbeddings, clusters)
	intraDistance, interDistance := calculateClusterDistances(filteredEmbeddings, clusters)
	clusterSizes := calculateClusterSizes(clusters)
	reporterDist := calculateReporterDistribution(clusters)
	crossReporterCount := calculateCrossReporterClusters(clusters)
	qualityAssessment := assessClusteringQuality(silhouetteScore, daviesBouldinIndex, len(clusters), len(filteredEmbeddings))
	importanceScores := calculateClusterImportance(clusters)

	// Create clustering result with enhanced metrics
	result := ClusteringResult{
		Clusters: clusters,
		Summary: ClusterSummary{
			TotalStories:          len(filteredEmbeddings),
			TotalClusters:         len(clusters),
			AverageSilhouette:     silhouetteScore,
			DaviesBouldinIndex:    daviesBouldinIndex,
			IntraClusterDistance:  intraDistance,
			InterClusterDistance:  interDistance,
			ClusterSizes:          clusterSizes,
			ReporterDistribution:  reporterDist,
			CrossReporterClusters: crossReporterCount,
			ClusterImportance:     importanceScores,
			QualityAssessment:     qualityAssessment,
		},
	}

	// Create clusters directory if it doesn't exist
	if err := os.MkdirAll("clusters", 0755); err != nil {
		return fmt.Errorf("failed to create clusters directory: %w", err)
	}

	// Save clustering results
	if err := saveClusters(result); err != nil {
		return fmt.Errorf("failed to save clusters: %w", err)
	}

	// Save detailed quality report
	if err := saveClusterQualityReport(result); err != nil {
		log.Printf("Warning: Failed to save quality report: %v", err)
	}

	// Print comprehensive clustering quality report
	printClusteringQualityReport(result)

	return nil
}

// loadAllEmbeddings loads all embeddings from the database
func loadAllEmbeddings(db *sql.DB) ([]EmbeddingRecord, error) {
	query := `
	SELECT story_id, video_id, title, summary, reporter, embedding_json
	FROM embeddings
	ORDER BY created_at
	`

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("Failed to close rows: %v", err)
		}
	}()

	var embeddings []EmbeddingRecord
	for rows.Next() {
		var record EmbeddingRecord
		var embeddingJSON string

		err := rows.Scan(&record.StoryID, &record.VideoID, &record.Title,
			&record.Summary, &record.Reporter, &embeddingJSON)
		if err != nil {
			return nil, err
		}

		// Parse embedding JSON
		if err := json.Unmarshal([]byte(embeddingJSON), &record.Embedding); err != nil {
			return nil, fmt.Errorf("failed to parse embedding for %s: %w", record.StoryID, err)
		}

		embeddings = append(embeddings, record)
	}

	return embeddings, rows.Err()
}

// normalizeEmbeddings applies L2 normalization to all embeddings for better clustering
func normalizeEmbeddings(embeddings []EmbeddingRecord) []EmbeddingRecord {
	normalized := make([]EmbeddingRecord, len(embeddings))

	for i, embedding := range embeddings {
		// Calculate L2 norm
		norm := 0.0
		for _, val := range embedding.Embedding {
			norm += val * val
		}
		norm = math.Sqrt(norm)

		// Normalize the embedding vector
		normalizedVec := make([]float64, len(embedding.Embedding))
		if norm > 0 {
			for j, val := range embedding.Embedding {
				normalizedVec[j] = val / norm
			}
		} else {
			copy(normalizedVec, embedding.Embedding)
		}

		normalized[i] = EmbeddingRecord{
			StoryID:   embedding.StoryID,
			VideoID:   embedding.VideoID,
			Title:     embedding.Title,
			Summary:   embedding.Summary,
			Reporter:  embedding.Reporter,
			Embedding: normalizedVec,
			CreatedAt: embedding.CreatedAt,
		}
	}

	return normalized
}

// tryStableDBSCAN performs DBSCAN with stability analysis
func tryStableDBSCAN(embeddings []EmbeddingRecord) ([]StoryCluster, bool) {
	// Run DBSCAN multiple times with slightly different parameters
	stabilityRuns := 3
	var allResults [][]StoryCluster

	baseEps := calculateOptimalEps(embeddings, 4) // Use 4th nearest neighbor for balanced clusters
	// Small variations for stable clustering
	epsVariations := []float64{baseEps * 0.95, baseEps, baseEps * 1.05}

	for i, eps := range epsVariations {
		log.Printf("🔄 Stability run %d/%d with eps=%.4f", i+1, stabilityRuns, eps)
		clusters, err := performDBSCANWithEps(embeddings, eps, 2) // minPts=2 for smaller clusters
		if err != nil || len(clusters) < 2 {
			continue
		}
		allResults = append(allResults, clusters)
	}

	if len(allResults) < 2 {
		log.Printf("⚠️  DBSCAN stability test failed - insufficient valid runs")
		return nil, false
	}

	// Analyze stability by comparing cluster assignments
	stabilityScore := calculateClusteringStability(embeddings, allResults)
	log.Printf("📏 DBSCAN stability score: %.3f", stabilityScore)

	if stabilityScore < 0.7 {
		log.Printf("⚠️  DBSCAN stability too low (%.3f < 0.7)", stabilityScore)
		return nil, false
	}

	// Return the result with best silhouette score
	bestClusters, bestScore := selectBestClustering(embeddings, allResults)

	// Check if we have too many single-story clusters
	singleCount := 0
	for _, cluster := range bestClusters {
		if len(cluster.Stories) == 1 {
			singleCount++
		}
	}

	// If too many singles, retry with higher eps (more lenient clustering)
	if singleCount >= 4 {
		log.Printf("⚠️  DBSCAN produced %d single-story clusters, retrying with higher eps", singleCount)

		// Retry with higher eps values
		higherEps := baseEps * 1.3
		retryResults := make([][]StoryCluster, 0, 2)

		for _, multiplier := range []float64{1.25, 1.35} {
			eps := higherEps * multiplier
			log.Printf("🔄 Retry with eps=%.4f", eps)

			retryClusters, err := performDBSCANWithEps(embeddings, eps, 2)
			if err == nil && len(retryClusters) >= 2 {
				retryResults = append(retryResults, retryClusters)
			}
		}

		if len(retryResults) > 0 {
			retryClusters, retryScore := selectBestClustering(embeddings, retryResults)
			retrySingleCount := 0
			for _, cluster := range retryClusters {
				if len(cluster.Stories) == 1 {
					retrySingleCount++
				}
			}

			// Use retry result if it has fewer singles or better score
			if retrySingleCount < singleCount || (retrySingleCount <= singleCount && retryScore > bestScore) {
				log.Printf("✅ Retry succeeded: %d singles (was %d), score %.3f (was %.3f)",
					retrySingleCount, singleCount, retryScore, bestScore)
				return retryClusters, true
			}
		}
	}

	return bestClusters, true
}

// performStableKMeansClustering performs K-means with stability analysis
func performStableKMeansClustering(embeddings []EmbeddingRecord, k int) ([]StoryCluster, error) {
	stabilityRuns := 5
	var allResults [][]StoryCluster

	log.Printf("🔄 Running %d K-means iterations for stability analysis...", stabilityRuns)

	for range stabilityRuns {
		// Use different random seeds for each run
		clusters, err := performKMeansClustering(embeddings, k)
		if err != nil {
			continue
		}
		allResults = append(allResults, clusters)
	}

	if len(allResults) == 0 {
		return nil, fmt.Errorf("all K-means runs failed")
	}

	// Analyze stability
	stabilityScore := calculateClusteringStability(embeddings, allResults)
	log.Printf("📏 K-means stability score: %.3f", stabilityScore)

	// Return the result with best silhouette score
	bestClusters, bestScore := selectBestClustering(embeddings, allResults)
	log.Printf("✅ Selected best clustering with silhouette score: %.3f", bestScore)

	return bestClusters, nil
}

// calculateClusteringStability measures how consistent cluster assignments are across runs
func calculateClusteringStability(embeddings []EmbeddingRecord, results [][]StoryCluster) float64 {
	if len(results) < 2 {
		return 1.0
	}

	// Create story ID to index mapping
	storyToIdx := make(map[string]int)
	for i, embedding := range embeddings {
		storyToIdx[embedding.StoryID] = i
	}

	totalAgreement := 0.0
	totalPairs := 0

	// Compare all pairs of clustering results
	for i := range len(results) {
		for j := i + 1; j < len(results); j++ {
			agreement := compareClusterAssignments(results[i], results[j])
			totalAgreement += agreement
			totalPairs++
		}
	}

	if totalPairs == 0 {
		return 1.0
	}

	return totalAgreement / float64(totalPairs)
}

// compareClusterAssignments compares two clustering results and returns agreement ratio
func compareClusterAssignments(clusters1, clusters2 []StoryCluster) float64 {
	// Create cluster assignment maps
	assign1 := make(map[string]int)
	assign2 := make(map[string]int)

	for cid, cluster := range clusters1 {
		for _, story := range cluster.Stories {
			assign1[story.StoryID] = cid
		}
	}

	for cid, cluster := range clusters2 {
		for _, story := range cluster.Stories {
			assign2[story.StoryID] = cid
		}
	}

	// Count agreements for all story pairs
	agreements := 0
	total := 0

	var storyIDs []string
	for storyID := range assign1 {
		if _, exists := assign2[storyID]; exists {
			storyIDs = append(storyIDs, storyID)
		}
	}

	for i := 0; i < len(storyIDs); i++ {
		for j := i + 1; j < len(storyIDs); j++ {
			story1, story2 := storyIDs[i], storyIDs[j]

			// Check if both clusterings agree on whether these stories are in the same cluster
			sameCluster1 := assign1[story1] == assign1[story2]
			sameCluster2 := assign2[story1] == assign2[story2]

			if sameCluster1 == sameCluster2 {
				agreements++
			}
			total++
		}
	}

	if total == 0 {
		return 1.0
	}

	return float64(agreements) / float64(total)
}

// selectBestClustering selects the clustering result with the best silhouette score
func selectBestClustering(embeddings []EmbeddingRecord, results [][]StoryCluster) ([]StoryCluster, float64) {
	bestClusters := results[0]
	bestScore := calculateSilhouetteScore(embeddings, results[0])

	for _, clusters := range results[1:] {
		score := calculateSilhouetteScore(embeddings, clusters)
		if score > bestScore {
			bestScore = score
			bestClusters = clusters
		}
	}

	return bestClusters, bestScore
}

// performDBSCANWithEps performs DBSCAN clustering with specified eps parameter
func performDBSCANWithEps(embeddings []EmbeddingRecord, eps float64, minPts int) ([]StoryCluster, error) {
	n := len(embeddings)
	if n < minPts {
		return nil, fmt.Errorf("too few embeddings for DBSCAN")
	}

	// DBSCAN algorithm
	visited := make([]bool, n)
	clusterID := make([]int, n)
	for i := range clusterID {
		clusterID[i] = -1 // -1 means noise/unassigned
	}

	currentCluster := 0

	for i := range n {
		if visited[i] {
			continue
		}
		visited[i] = true

		// Find neighbors
		neighbors := findNeighbors(embeddings, i, eps)

		if len(neighbors) < minPts {
			// Mark as noise
			clusterID[i] = -1
		} else {
			// Start new cluster
			expandCluster(embeddings, i, neighbors, currentCluster, eps, minPts, visited, clusterID)
			currentCluster++
		}
	}

	// Convert to cluster format
	clusters := make(map[int][]int)
	for i, cid := range clusterID {
		if cid >= 0 { // Ignore noise points
			clusters[cid] = append(clusters[cid], i)
		}
	}

	if len(clusters) == 0 {
		return nil, fmt.Errorf("DBSCAN found no valid clusters")
	}

	// Create story clusters
	var storyClusters []StoryCluster
	for cid, indices := range clusters {
		if len(indices) == 0 {
			continue
		}

		cluster := StoryCluster{
			ClusterID: cid,
			Stories:   make([]ClusteredStory, 0, len(indices)),
		}

		// Calculate cluster centroid
		embeddingDim := len(embeddings[0].Embedding)
		centroid := make([]float64, embeddingDim)
		for _, idx := range indices {
			for j, val := range embeddings[idx].Embedding {
				centroid[j] += val
			}
		}
		for j := range centroid {
			centroid[j] /= float64(len(indices))
		}
		cluster.Centroid = centroid

		// Add stories to cluster
		for _, idx := range indices {
			embedding := embeddings[idx]
			similarity := cosineSimilarity(embedding.Embedding, centroid)

			story := ClusteredStory{
				StoryID:    embedding.StoryID,
				VideoID:    embedding.VideoID,
				Title:      embedding.Title,
				Summary:    embedding.Summary,
				Reporter:   embedding.Reporter,
				Similarity: similarity,
			}
			cluster.Stories = append(cluster.Stories, story)
		}

		storyClusters = append(storyClusters, cluster)
	}

	// Sort clusters by size (largest first)
	sort.Slice(storyClusters, func(i, j int) bool {
		return len(storyClusters[i].Stories) > len(storyClusters[j].Stories)
	})

	// Reassign cluster IDs
	for i := range storyClusters {
		storyClusters[i].ClusterID = i
	}

	return storyClusters, nil
}

// performDBSCANClustering performs DBSCAN clustering for natural cluster discovery

// calculateOptimalEps calculates optimal eps parameter using adaptive approach
// Uses elbow detection on k-distance graph for better automatic tuning
func calculateOptimalEps(embeddings []EmbeddingRecord, k int) float64 {
	n := len(embeddings)
	kDistances := make([]float64, n)

	for i := range n {
		distances := make([]float64, 0, n-1)
		for j := range n {
			if i != j {
				// Use cosine distance (1 - cosine similarity) for normalized vectors
				sim := cosineSimilarity(embeddings[i].Embedding, embeddings[j].Embedding)
				dist := 1.0 - sim
				distances = append(distances, dist)
			}
		}

		// Sort distances and take k-th nearest
		sort.Float64s(distances)
		if k-1 < len(distances) {
			kDistances[i] = distances[k-1]
		} else if len(distances) > 0 {
			kDistances[i] = distances[len(distances)-1]
		}
	}

	// Sort k-distances
	sort.Float64s(kDistances)

	// Use adaptive percentile based on dataset size
	// Smaller datasets need higher percentile to avoid over-fragmentation
	// Larger datasets can use lower percentile for finer granularity
	var percentile float64
	if n < 20 {
		percentile = 0.3 // 30th percentile for small datasets
	} else if n < 50 {
		percentile = 0.25 // 25th percentile for medium datasets
	} else {
		percentile = 0.15 // 15th percentile for large datasets
	}

	elbowIdx := int(float64(n) * percentile)
	if elbowIdx >= n {
		elbowIdx = n - 1
	}
	if elbowIdx < 1 {
		elbowIdx = 1
	}

	eps := kDistances[elbowIdx]

	// Adaptive bounds based on dataset characteristics
	// Calculate mean and std of k-distances for intelligent bounds
	mean := 0.0
	for _, d := range kDistances {
		mean += d
	}
	mean /= float64(len(kDistances))

	stdDev := 0.0
	for _, d := range kDistances {
		diff := d - mean
		stdDev += diff * diff
	}
	stdDev = math.Sqrt(stdDev / float64(len(kDistances)))

	// Set bounds at mean ± 2*stdDev, with reasonable limits
	minEps := math.Max(0.03, mean-2*stdDev)
	maxEps := math.Min(0.35, mean+stdDev)

	if eps < minEps {
		eps = minEps
	} else if eps > maxEps {
		eps = maxEps
	}

	log.Printf("🎯 Calculated eps=%.4f (%.0fth percentile, mean=%.4f, std=%.4f, bounds=[%.4f, %.4f])",
		eps, percentile*100, mean, stdDev, minEps, maxEps)
	return eps
}

// findNeighbors finds all points within eps distance of the given point
func findNeighbors(embeddings []EmbeddingRecord, pointIdx int, eps float64) []int {
	var neighbors []int
	point := embeddings[pointIdx]

	for i, other := range embeddings {
		if i != pointIdx {
			// Use cosine distance for normalized vectors
			sim := cosineSimilarity(point.Embedding, other.Embedding)
			dist := 1.0 - sim
			if dist <= eps {
				neighbors = append(neighbors, i)
			}
		}
	}

	return neighbors
}

// expandCluster expands a cluster using DBSCAN algorithm
func expandCluster(embeddings []EmbeddingRecord, pointIdx int, neighbors []int, clusterID int, eps float64, minPts int, visited []bool, pointClusterID []int) {
	pointClusterID[pointIdx] = clusterID

	for i := 0; i < len(neighbors); i++ {
		nIdx := neighbors[i]

		if !visited[nIdx] {
			visited[nIdx] = true
			newNeighbors := findNeighbors(embeddings, nIdx, eps)
			if len(newNeighbors) >= minPts {
				// Add new neighbors to the list
				for _, newN := range newNeighbors {
					// Check if already in neighbors list
					alreadyIn := false
					for _, existing := range neighbors {
						if existing == newN {
							alreadyIn = true
							break
						}
					}
					if !alreadyIn {
						neighbors = append(neighbors, newN)
					}
				}
			}
		}

		if pointClusterID[nIdx] == -1 {
			pointClusterID[nIdx] = clusterID
		}
	}
}

// removeOutliers detects and removes outlier stories that don't fit well with others
// Uses isolation-based approach: stories with very low similarity to all others
func removeOutliers(embeddings []EmbeddingRecord) ([]EmbeddingRecord, int) {
	if len(embeddings) < 10 {
		return embeddings, 0 // Don't remove outliers from very small datasets
	}

	// Calculate average similarity of each story to all others
	avgSimilarities := make([]float64, len(embeddings))

	for i, embedding1 := range embeddings {
		totalSimilarity := 0.0
		count := 0

		for j, embedding2 := range embeddings {
			if i != j {
				similarity := cosineSimilarity(embedding1.Embedding, embedding2.Embedding)
				totalSimilarity += similarity
				count++
			}
		}

		if count > 0 {
			avgSimilarities[i] = totalSimilarity / float64(count)
		}
	}

	// Calculate mean and standard deviation of similarities
	mean := 0.0
	for _, sim := range avgSimilarities {
		mean += sim
	}
	mean /= float64(len(avgSimilarities))

	variance := 0.0
	for _, sim := range avgSimilarities {
		diff := sim - mean
		variance += diff * diff
	}
	variance /= float64(len(avgSimilarities))
	stdDev := math.Sqrt(variance)

	// Remove outliers: stories more than 2 standard deviations below mean
	// (i.e., stories that are very dissimilar to everything else)
	threshold := mean - 2.0*stdDev

	var filtered []EmbeddingRecord
	removedCount := 0

	for i, embedding := range embeddings {
		if avgSimilarities[i] >= threshold {
			filtered = append(filtered, embedding)
		} else {
			log.Printf("🗑️  Outlier removed: %s (avg similarity: %.3f < threshold: %.3f)",
				embedding.Title, avgSimilarities[i], threshold)
			removedCount++
		}
	}

	// Don't remove too many stories (max 20% of dataset)
	maxRemoval := len(embeddings) / 5
	if removedCount > maxRemoval {
		log.Printf("⚠️  Too many outliers detected (%d), keeping top %d stories instead",
			removedCount, len(embeddings)-maxRemoval)

		// Sort by similarity and keep the best ones
		type indexedEmbedding struct {
			embedding  EmbeddingRecord
			similarity float64
			index      int
		}

		var indexed []indexedEmbedding
		for i, embedding := range embeddings {
			indexed = append(indexed, indexedEmbedding{
				embedding:  embedding,
				similarity: avgSimilarities[i],
				index:      i,
			})
		}

		// Sort by similarity (highest first)
		for i := 0; i < len(indexed)-1; i++ {
			for j := i + 1; j < len(indexed); j++ {
				if indexed[i].similarity < indexed[j].similarity {
					indexed[i], indexed[j] = indexed[j], indexed[i]
				}
			}
		}

		// Take top stories
		keepCount := len(embeddings) - maxRemoval
		filtered = make([]EmbeddingRecord, keepCount)
		for i := 0; i < keepCount; i++ {
			filtered[i] = indexed[i].embedding
		}
		removedCount = maxRemoval
	}

	return filtered, removedCount
}

// findOptimalK uses comprehensive evaluation to determine the best number of clusters
func findOptimalK(embeddings []EmbeddingRecord) int {
	numStories := len(embeddings)

	// Determine K range to test - allow higher K for better granularity
	minK := 2
	maxK := int(math.Min(float64(int(math.Sqrt(float64(numStories))*1.5)), 15)) // Increased range
	if maxK < minK {
		maxK = minK
	}
	if numStories < 4 {
		return minK // For very small datasets
	}

	log.Printf("🔍 Evaluating K from %d to %d clusters...", minK, maxK)

	var results []KEvaluationResult

	// Test each K value
	for k := minK; k <= maxK; k++ {
		clusters, err := performKMeansClustering(embeddings, k)
		if err != nil {
			log.Printf("Failed to cluster with k=%d: %v", k, err)
			continue
		}

		// Calculate all quality metrics
		silhouette := calculateSilhouetteScore(embeddings, clusters)
		daviesBouldin := calculateDaviesBouldinIndex(embeddings, clusters)
		calinskiHarabasz := calculateCalinskiHarabasz(embeddings, clusters)
		wcss := calculateWCSS(embeddings, clusters)
		crossReporter := float64(calculateCrossReporterClusters(clusters)) / float64(len(clusters))
		coherence := calculateClusterCoherence(embeddings, clusters)
		clusterSizes := calculateClusterSizes(clusters)

		// Optimized news-aware scoring: Silhouette 35%, Coherence 30%, Davies-Bouldin 25%, Cross-Reporter 10%
		// Prioritize semantic coherence since it's our strongest metric
		normalizedDB := 1.0 / (1.0 + daviesBouldin)
		baseScore := 0.35*silhouette + 0.30*coherence + 0.25*normalizedDB + 0.10*crossReporter

		// Penalty for oversimplification: discourage K=2,3 for larger datasets
		simplicityPenalty := 0.0
		if k <= 3 && len(embeddings) > 20 {
			simplicityPenalty = 0.1 * (4.0 - float64(k)) // 0.2 penalty for K=2, 0.1 for K=3
		}

		// Penalty for micro-clusters: discourage solutions with too many single-story clusters
		microClusterPenalty := 0.0
		singleClusterCount := 0
		for _, size := range clusterSizes {
			if size == 1 {
				singleClusterCount++
			}
		}

		// Progressive penalty: mild for 1-2 singles, harsh for 3+
		if singleClusterCount >= 3 {
			microClusterPenalty = 0.15 // Heavy penalty for 3+ single clusters
		} else if singleClusterCount == 2 {
			microClusterPenalty = 0.05 // Light penalty for 2 single clusters
		}
		// No penalty for 0-1 single clusters (acceptable)

		// Bonus for balanced cluster sizes (prefer 2-15 stories per cluster)
		balancePenalty := 0.0
		avgClusterSize := float64(len(embeddings)) / float64(k)
		if avgClusterSize < 1.5 {
			balancePenalty = 0.08 // Too many micro-clusters
		} else if avgClusterSize > 20 {
			balancePenalty = 0.05 // Too few mega-clusters
		}

		weightedScore := baseScore - simplicityPenalty - microClusterPenalty - balancePenalty

		result := KEvaluationResult{
			K:                     k,
			SilhouetteScore:       silhouette,
			DaviesBouldinIndex:    daviesBouldin,
			CalinskiHarabaszIndex: calinskiHarabasz,
			WCSS:                  wcss,
			CrossReporterRatio:    crossReporter,
			ClusterCoherence:      coherence,
			WeightedScore:         weightedScore,
			ClusterSizes:          clusterSizes,
		}

		results = append(results, result)

		penaltyStr := ""
		totalPenalty := simplicityPenalty + microClusterPenalty + balancePenalty
		if totalPenalty > 0 {
			penaltyStr = fmt.Sprintf(" (-%.3f penalty)", totalPenalty)
			if microClusterPenalty > 0 {
				penaltyStr += fmt.Sprintf(" [%d singles]", singleClusterCount)
			}
		}
		log.Printf("  K=%d: Silh=%.3f, DB=%.3f, CH=%.1f, Coh=%.3f, Cross=%.0f%%, Score=%.3f%s",
			k, silhouette, daviesBouldin, calinskiHarabasz, coherence, crossReporter*100, weightedScore, penaltyStr)
	}

	// Apply evaluation strategy: filter by cross-reporter effectiveness first
	var validResults []KEvaluationResult
	for _, result := range results {
		if result.CrossReporterRatio >= 0.7 { // Must have ≥70% cross-reporter effectiveness
			validResults = append(validResults, result)
		}
	}

	// If no results meet the threshold, relax to ≥60%
	if len(validResults) == 0 {
		log.Printf("⚠️  No K values with >=70%% cross-reporter effectiveness, relaxing to >=60%%")
		for _, result := range results {
			if result.CrossReporterRatio >= 0.6 {
				validResults = append(validResults, result)
			}
		}
	}

	// If still none, use all results
	if len(validResults) == 0 {
		log.Printf("⚠️  Using all K values - cross-reporter filtering too strict")
		validResults = results
	}

	// Find the K with the highest weighted score from valid results
	bestK := minK
	bestScore := -1.0

	for _, result := range validResults {
		if result.WeightedScore > bestScore {
			bestScore = result.WeightedScore
			bestK = result.K
		}
	}

	log.Printf("🎯 Selected optimal K=%d (weighted score: %.3f)", bestK, bestScore)
	log.Printf("📊 K Evaluation Summary:")
	for _, result := range results {
		marker := "  "
		if result.K == bestK {
			marker = "→ "
		}
		log.Printf("%sK=%d: Score=%.3f (Silh=%.3f, DB=%.3f, Coh=%.3f, Cross=%.0f%%)",
			marker, result.K, result.WeightedScore, result.SilhouetteScore,
			result.DaviesBouldinIndex, result.ClusterCoherence, result.CrossReporterRatio*100)
	}

	return bestK
}

// performKMeansClustering performs k-means clustering on embeddings
func performKMeansClustering(embeddings []EmbeddingRecord, k int) ([]StoryCluster, error) {
	if k >= len(embeddings) {
		k = len(embeddings)
	}

	// Convert embeddings to matrix format
	embeddingDim := len(embeddings[0].Embedding)
	dataMatrix := mat.NewDense(len(embeddings), embeddingDim, nil)

	for i, embedding := range embeddings {
		dataMatrix.SetRow(i, embedding.Embedding)
	}

	// Initialize centroids using k-means++
	centroids := initializeCentroidsKMeansPlusPlus(dataMatrix, k)

	// Perform k-means iterations
	maxIterations := 100
	tolerance := 1e-4

	assignments := make([]int, len(embeddings))

	for iteration := 0; iteration < maxIterations; iteration++ {
		// Assign points to clusters
		newAssignments := assignPointsToClusters(dataMatrix, centroids)

		// Check for convergence
		converged := true
		for i := range assignments {
			if assignments[i] != newAssignments[i] {
				converged = false
				break
			}
		}
		assignments = newAssignments

		if converged {
			log.Printf("K-means converged after %d iterations", iteration+1)
			break
		}

		// Update centroids
		newCentroids := updateCentroids(dataMatrix, assignments, k)

		// Check centroid change
		centroidChange := calculateCentroidChange(centroids, newCentroids)
		centroids = newCentroids

		if centroidChange < tolerance {
			log.Printf("K-means converged due to small centroid change after %d iterations", iteration+1)
			break
		}
	}

	// Create clusters from assignments
	clusters := make([]StoryCluster, k)
	for i := range clusters {
		clusters[i].ClusterID = i
		clusters[i].Centroid = make([]float64, embeddingDim)
		copy(clusters[i].Centroid, centroids.RawRowView(i))
	}

	// Populate clusters with stories
	for i, embedding := range embeddings {
		clusterID := assignments[i]

		// Calculate similarity to centroid
		similarity := cosineSimilarity(embedding.Embedding, clusters[clusterID].Centroid)

		clusteredStory := ClusteredStory{
			StoryID:    embedding.StoryID,
			VideoID:    embedding.VideoID,
			Title:      embedding.Title,
			Summary:    embedding.Summary,
			Reporter:   embedding.Reporter,
			Similarity: similarity,
		}

		clusters[clusterID].Stories = append(clusters[clusterID].Stories, clusteredStory)
	}

	// Remove empty clusters
	var nonEmptyClusters []StoryCluster
	for _, cluster := range clusters {
		if len(cluster.Stories) > 0 {
			nonEmptyClusters = append(nonEmptyClusters, cluster)
		}
	}

	return nonEmptyClusters, nil
}

// performHierarchicalClustering performs agglomerative hierarchical clustering
// Uses average linkage with cosine distance for better semantic grouping
func performHierarchicalClustering(embeddings []EmbeddingRecord, k int) ([]StoryCluster, error) {
	n := len(embeddings)
	if k >= n {
		k = n
	}
	if k < 2 {
		return nil, fmt.Errorf("k must be at least 2")
	}

	// Initialize: each point is its own cluster
	clusters := make([][]int, n)
	for i := 0; i < n; i++ {
		clusters[i] = []int{i}
	}

	// Calculate initial distance matrix using cosine distance
	distMatrix := make([][]float64, n)
	for i := 0; i < n; i++ {
		distMatrix[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			if i != j {
				sim := cosineSimilarity(embeddings[i].Embedding, embeddings[j].Embedding)
				distMatrix[i][j] = 1.0 - sim // Convert to distance
			}
		}
	}

	// Merge clusters until we have k clusters
	for len(clusters) > k {
		// Find the pair of clusters with minimum average distance
		minDist := math.Inf(1)
		mergeI, mergeJ := -1, -1

		for i := 0; i < len(clusters); i++ {
			for j := i + 1; j < len(clusters); j++ {
				// Calculate average linkage distance between clusters
				avgDist := 0.0
				count := 0
				for _, idxI := range clusters[i] {
					for _, idxJ := range clusters[j] {
						avgDist += distMatrix[idxI][idxJ]
						count++
					}
				}
				if count > 0 {
					avgDist /= float64(count)
				}

				if avgDist < minDist {
					minDist = avgDist
					mergeI = i
					mergeJ = j
				}
			}
		}

		if mergeI == -1 || mergeJ == -1 {
			break // No more merges possible
		}

		// Merge cluster j into cluster i
		clusters[mergeI] = append(clusters[mergeI], clusters[mergeJ]...)

		// Remove cluster j
		clusters = append(clusters[:mergeJ], clusters[mergeJ+1:]...)

		log.Printf("🔗 Merged 2 clusters (avg dist: %.4f), %d clusters remaining", minDist, len(clusters))
	}

	// Convert to StoryCluster format
	storyClusters := make([]StoryCluster, len(clusters))
	embeddingDim := len(embeddings[0].Embedding)

	for i, clusterIndices := range clusters {
		cluster := StoryCluster{
			ClusterID: i,
			Stories:   make([]ClusteredStory, 0, len(clusterIndices)),
		}

		// Calculate centroid
		centroid := make([]float64, embeddingDim)
		for _, idx := range clusterIndices {
			for j, val := range embeddings[idx].Embedding {
				centroid[j] += val
			}
		}
		for j := range centroid {
			centroid[j] /= float64(len(clusterIndices))
		}
		cluster.Centroid = centroid

		// Add stories
		for _, idx := range clusterIndices {
			embedding := embeddings[idx]
			similarity := cosineSimilarity(embedding.Embedding, centroid)

			story := ClusteredStory{
				StoryID:    embedding.StoryID,
				VideoID:    embedding.VideoID,
				Title:      embedding.Title,
				Summary:    embedding.Summary,
				Reporter:   embedding.Reporter,
				Similarity: similarity,
			}
			cluster.Stories = append(cluster.Stories, story)
		}

		storyClusters[i] = cluster
	}

	// Sort clusters by size (largest first)
	sort.Slice(storyClusters, func(i, j int) bool {
		return len(storyClusters[i].Stories) > len(storyClusters[j].Stories)
	})

	// Reassign cluster IDs after sorting
	for i := range storyClusters {
		storyClusters[i].ClusterID = i
	}

	return storyClusters, nil
}

// initializeCentroidsKMeansPlusPlus initializes centroids using improved k-means++ method
func initializeCentroidsKMeansPlusPlus(data *mat.Dense, k int) *mat.Dense {
	n, d := data.Dims()
	centroids := mat.NewDense(k, d, nil)

	// Choose first centroid randomly
	firstIdx := rand.Intn(n)
	centroids.SetRow(0, data.RawRowView(firstIdx))

	// Choose remaining centroids
	for i := 1; i < k; i++ {
		distances := make([]float64, n)

		// Calculate distance to nearest centroid for each point
		for j := 0; j < n; j++ {
			point := data.RawRowView(j)
			minDist := math.Inf(1)

			for c := 0; c < i; c++ {
				centroid := centroids.RawRowView(c)
				// Use cosine distance for better semantic separation
				sim := cosineSimilarity(point, centroid)
				dist := 1.0 - sim
				if dist < minDist {
					minDist = dist
				}
			}
			distances[j] = minDist * minDist // Square for weighted probability
		}

		// Choose next centroid based on weighted probability
		totalWeight := 0.0
		for _, dist := range distances {
			totalWeight += dist
		}

		if totalWeight == 0 {
			// Fallback: choose randomly if all points are identical
			centroids.SetRow(i, data.RawRowView(rand.Intn(n)))
			continue
		}

		target := rand.Float64() * totalWeight
		cumWeight := 0.0

		for j, dist := range distances {
			cumWeight += dist
			if cumWeight >= target {
				centroids.SetRow(i, data.RawRowView(j))
				break
			}
		}
	}

	return centroids
}

// assignPointsToClusters assigns each data point to the nearest cluster using improved distance metrics
func assignPointsToClusters(data *mat.Dense, centroids *mat.Dense) []int {
	n, _ := data.Dims()
	k, _ := centroids.Dims()
	assignments := make([]int, n)

	for i := 0; i < n; i++ {
		point := data.RawRowView(i)
		minDist := math.Inf(1)
		bestCluster := 0

		for j := 0; j < k; j++ {
			centroid := centroids.RawRowView(j)
			// Use cosine distance for normalized vectors (better for semantic similarity)
			sim := cosineSimilarity(point, centroid)
			dist := 1.0 - sim // Convert similarity to distance

			if dist < minDist {
				minDist = dist
				bestCluster = j
			}
		}

		assignments[i] = bestCluster
	}

	return assignments
}

// updateCentroids recalculates cluster centroids
func updateCentroids(data *mat.Dense, assignments []int, k int) *mat.Dense {
	n, d := data.Dims()
	centroids := mat.NewDense(k, d, nil)
	counts := make([]int, k)

	// Sum points in each cluster
	for i := 0; i < n; i++ {
		clusterID := assignments[i]
		point := data.RawRowView(i)

		for j := 0; j < d; j++ {
			centroids.Set(clusterID, j, centroids.At(clusterID, j)+point[j])
		}
		counts[clusterID]++
	}

	// Average to get centroids
	for i := 0; i < k; i++ {
		if counts[i] > 0 {
			for j := 0; j < d; j++ {
				centroids.Set(i, j, centroids.At(i, j)/float64(counts[i]))
			}
		}
	}

	return centroids
}

// calculateCentroidChange calculates the total change in centroids using cosine distance
func calculateCentroidChange(oldCentroids, newCentroids *mat.Dense) float64 {
	k, _ := oldCentroids.Dims()
	totalChange := 0.0

	for i := 0; i < k; i++ {
		oldCentroid := oldCentroids.RawRowView(i)
		newCentroid := newCentroids.RawRowView(i)
		// Use cosine distance for consistency with our distance metric
		sim := cosineSimilarity(oldCentroid, newCentroid)
		change := 1.0 - sim
		totalChange += change
	}

	return totalChange / float64(k)
}

// cosineSimilarity calculates cosine similarity between two vectors
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}

	dotProduct := 0.0
	normA := 0.0
	normB := 0.0

	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}


// calculateSilhouetteScore calculates the average silhouette score for clustering quality
// Uses cosine distance for consistency with embedding space
func calculateSilhouetteScore(embeddings []EmbeddingRecord, clusters []StoryCluster) float64 {
	if len(clusters) <= 1 {
		return 0.0
	}

	// Create embedding lookup
	embeddingMap := make(map[string][]float64)
	clusterMap := make(map[string]int)

	for _, embedding := range embeddings {
		embeddingMap[embedding.StoryID] = embedding.Embedding
	}

	for clusterID, cluster := range clusters {
		for _, story := range cluster.Stories {
			clusterMap[story.StoryID] = clusterID
		}
	}

	totalSilhouette := 0.0

	for _, embedding := range embeddings {
		storyID := embedding.StoryID
		clusterID := clusterMap[storyID]

		// Calculate average distance to points in same cluster (a)
		a := 0.0
		sameClusterCount := 0
		for _, story := range clusters[clusterID].Stories {
			if story.StoryID != storyID {
				// Use cosine distance (1 - cosine similarity) for semantic embeddings
				sim := cosineSimilarity(embedding.Embedding, embeddingMap[story.StoryID])
				distance := 1.0 - sim
				a += distance
				sameClusterCount++
			}
		}
		if sameClusterCount > 0 {
			a /= float64(sameClusterCount)
		}

		// Calculate minimum average distance to points in other clusters (b)
		b := math.Inf(1)
		for otherClusterID, otherCluster := range clusters {
			if otherClusterID != clusterID {
				avgDistance := 0.0
				for _, story := range otherCluster.Stories {
					// Use cosine distance
					sim := cosineSimilarity(embedding.Embedding, embeddingMap[story.StoryID])
					distance := 1.0 - sim
					avgDistance += distance
				}
				avgDistance /= float64(len(otherCluster.Stories))
				if avgDistance < b {
					b = avgDistance
				}
			}
		}

		// Calculate silhouette score for this point
		silhouette := 0.0
		if math.Max(a, b) > 0 {
			silhouette = (b - a) / math.Max(a, b)
		}

		totalSilhouette += silhouette
	}

	return totalSilhouette / float64(len(embeddings))
}

// saveClusters saves the clustering results to JSON file
func saveClusters(result ClusteringResult) error {
	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal clusters: %w", err)
	}

	if err := os.WriteFile("clusters/clusters.json", jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write clusters file: %w", err)
	}

	return nil
}

// calculateDaviesBouldinIndex calculates the Davies-Bouldin index for clustering quality
// Uses cosine distance for consistency with embedding space
func calculateDaviesBouldinIndex(embeddings []EmbeddingRecord, clusters []StoryCluster) float64 {
	if len(clusters) <= 1 {
		return 0.0
	}

	// Create embedding lookup
	embeddingMap := make(map[string][]float64)
	clusterMap := make(map[string]int)

	for _, embedding := range embeddings {
		embeddingMap[embedding.StoryID] = embedding.Embedding
	}

	for clusterID, cluster := range clusters {
		for _, story := range cluster.Stories {
			clusterMap[story.StoryID] = clusterID
		}
	}

	// Calculate centroids for each cluster
	centroids := make([][]float64, len(clusters))
	for i, cluster := range clusters {
		if len(cluster.Stories) == 0 {
			continue
		}

		// Calculate centroid
		dim := len(embeddingMap[cluster.Stories[0].StoryID])
		centroid := make([]float64, dim)

		for _, story := range cluster.Stories {
			embedding := embeddingMap[story.StoryID]
			for j := 0; j < dim; j++ {
				centroid[j] += embedding[j]
			}
		}

		for j := 0; j < dim; j++ {
			centroid[j] /= float64(len(cluster.Stories))
		}

		centroids[i] = centroid
	}

	// Calculate average intra-cluster distances using cosine distance
	intraClusterDistances := make([]float64, len(clusters))
	for i, cluster := range clusters {
		if len(cluster.Stories) <= 1 {
			intraClusterDistances[i] = 0
			continue
		}

		totalDistance := 0.0
		count := 0

		for _, story1 := range cluster.Stories {
			for _, story2 := range cluster.Stories {
				if story1.StoryID != story2.StoryID {
					// Use cosine distance
					sim := cosineSimilarity(embeddingMap[story1.StoryID], embeddingMap[story2.StoryID])
					dist := 1.0 - sim
					totalDistance += dist
					count++
				}
			}
		}

		intraClusterDistances[i] = totalDistance / float64(count)
	}

	// Calculate Davies-Bouldin index using cosine distance for centroids
	dbIndex := 0.0
	for i := 0; i < len(clusters); i++ {
		maxRatio := 0.0

		for j := 0; j < len(clusters); j++ {
			if i != j {
				// Use cosine distance for centroid separation
				sim := cosineSimilarity(centroids[i], centroids[j])
				centroidDistance := 1.0 - sim
				if centroidDistance > 0 {
					ratio := (intraClusterDistances[i] + intraClusterDistances[j]) / centroidDistance
					if ratio > maxRatio {
						maxRatio = ratio
					}
				}
			}
		}

		dbIndex += maxRatio
	}

	return dbIndex / float64(len(clusters))
}

// calculateClusterDistances calculates average intra and inter-cluster distances
// Uses cosine distance for consistency with embedding space
func calculateClusterDistances(embeddings []EmbeddingRecord, clusters []StoryCluster) (float64, float64) {
	embeddingMap := make(map[string][]float64)
	clusterMap := make(map[string]int)

	for _, embedding := range embeddings {
		embeddingMap[embedding.StoryID] = embedding.Embedding
	}

	for clusterID, cluster := range clusters {
		for _, story := range cluster.Stories {
			clusterMap[story.StoryID] = clusterID
		}
	}

	// Calculate intra-cluster distance using cosine distance
	totalIntraDistance := 0.0
	intraCount := 0

	for _, cluster := range clusters {
		for _, story1 := range cluster.Stories {
			for _, story2 := range cluster.Stories {
				if story1.StoryID != story2.StoryID {
					sim := cosineSimilarity(embeddingMap[story1.StoryID], embeddingMap[story2.StoryID])
					dist := 1.0 - sim
					totalIntraDistance += dist
					intraCount++
				}
			}
		}
	}

	avgIntraDistance := 0.0
	if intraCount > 0 {
		avgIntraDistance = totalIntraDistance / float64(intraCount)
	}

	// Calculate inter-cluster distance using cosine distance
	totalInterDistance := 0.0
	interCount := 0

	for i, cluster1 := range clusters {
		for j, cluster2 := range clusters {
			if i < j { // Avoid double counting
				for _, story1 := range cluster1.Stories {
					for _, story2 := range cluster2.Stories {
						sim := cosineSimilarity(embeddingMap[story1.StoryID], embeddingMap[story2.StoryID])
						dist := 1.0 - sim
						totalInterDistance += dist
						interCount++
					}
				}
			}
		}
	}

	avgInterDistance := 0.0
	if interCount > 0 {
		avgInterDistance = totalInterDistance / float64(interCount)
	}

	return avgIntraDistance, avgInterDistance
}

// calculateClusterSizes returns the sizes of each cluster
func calculateClusterSizes(clusters []StoryCluster) []int {
	sizes := make([]int, len(clusters))
	for i, cluster := range clusters {
		sizes[i] = len(cluster.Stories)
	}
	return sizes
}

// calculateReporterDistribution calculates how many stories each reporter has across all clusters
func calculateReporterDistribution(clusters []StoryCluster) map[string]int {
	distribution := make(map[string]int)

	for _, cluster := range clusters {
		for _, story := range cluster.Stories {
			distribution[story.Reporter]++
		}
	}

	return distribution
}

// calculateCrossReporterClusters counts how many clusters contain stories from multiple reporters
func calculateCrossReporterClusters(clusters []StoryCluster) int {
	crossReporterCount := 0

	for _, cluster := range clusters {
		reporters := make(map[string]bool)
		for _, story := range cluster.Stories {
			reporters[story.Reporter] = true
		}

		if len(reporters) > 1 {
			crossReporterCount++
		}
	}

	return crossReporterCount
}

// calculateClusterImportance calculates newsworthiness score for each cluster
// Based on cluster size (story count) and reporter diversity
func calculateClusterImportance(clusters []StoryCluster) []float64 {
	importanceScores := make([]float64, len(clusters))

	// Find max cluster size for normalization
	maxSize := 0
	for _, cluster := range clusters {
		if len(cluster.Stories) > maxSize {
			maxSize = len(cluster.Stories)
		}
	}

	for i, cluster := range clusters {
		if len(cluster.Stories) == 0 {
			importanceScores[i] = 0.0
			continue
		}

		// Size component (normalized): larger clusters = more important stories
		sizeScore := float64(len(cluster.Stories)) / float64(maxSize)

		// Reporter diversity component: more reporters = more newsworthy
		reporterCount := make(map[string]bool)
		for _, story := range cluster.Stories {
			reporterCount[story.Reporter] = true
		}
		diversityScore := float64(len(reporterCount)) / 6.0 // Max 6 reporters in dataset

		// Combined importance: 70% size, 30% diversity
		// Major stories with wide coverage get highest scores
		importanceScores[i] = 0.7*sizeScore + 0.3*diversityScore
	}

	return importanceScores
}

// assessClusteringQuality provides a qualitative assessment of clustering results
// calculateWCSS calculates Within-Cluster Sum of Squares for elbow method
func calculateWCSS(embeddings []EmbeddingRecord, clusters []StoryCluster) float64 {
	embeddingMap := make(map[string][]float64)
	for _, embedding := range embeddings {
		embeddingMap[embedding.StoryID] = embedding.Embedding
	}

	wcss := 0.0
	for _, cluster := range clusters {
		// Calculate cluster centroid
		if len(cluster.Stories) == 0 {
			continue
		}

		embeddingDim := len(embeddingMap[cluster.Stories[0].StoryID])
		centroid := make([]float64, embeddingDim)

		for _, story := range cluster.Stories {
			embedding := embeddingMap[story.StoryID]
			for i, val := range embedding {
				centroid[i] += val
			}
		}
		for i := range centroid {
			centroid[i] /= float64(len(cluster.Stories))
		}

		// Sum squared distances to centroid
		for _, story := range cluster.Stories {
			embedding := embeddingMap[story.StoryID]
			distance := 0.0
			for i, val := range embedding {
				diff := val - centroid[i]
				distance += diff * diff
			}
			wcss += distance
		}
	}

	return wcss
}

// calculateCalinskiHarabasz calculates the Calinski-Harabasz index (variance ratio criterion)
func calculateCalinskiHarabasz(embeddings []EmbeddingRecord, clusters []StoryCluster) float64 {
	if len(clusters) <= 1 {
		return 0.0
	}

	embeddingMap := make(map[string][]float64)
	for _, embedding := range embeddings {
		embeddingMap[embedding.StoryID] = embedding.Embedding
	}

	// Calculate overall centroid
	embeddingDim := len(embeddings[0].Embedding)
	overallCentroid := make([]float64, embeddingDim)
	totalPoints := 0

	for _, cluster := range clusters {
		for _, story := range cluster.Stories {
			embedding := embeddingMap[story.StoryID]
			for i, val := range embedding {
				overallCentroid[i] += val
			}
			totalPoints++
		}
	}
	for i := range overallCentroid {
		overallCentroid[i] /= float64(totalPoints)
	}

	// Calculate between-cluster sum of squares (BCSS)
	bcss := 0.0
	for _, cluster := range clusters {
		if len(cluster.Stories) == 0 {
			continue
		}

		// Calculate cluster centroid
		centroid := make([]float64, embeddingDim)
		for _, story := range cluster.Stories {
			embedding := embeddingMap[story.StoryID]
			for i, val := range embedding {
				centroid[i] += val
			}
		}
		for i := range centroid {
			centroid[i] /= float64(len(cluster.Stories))
		}

		// Distance from cluster centroid to overall centroid
		distance := 0.0
		for i, val := range centroid {
			diff := val - overallCentroid[i]
			distance += diff * diff
		}
		bcss += float64(len(cluster.Stories)) * distance
	}

	// Calculate within-cluster sum of squares (same as WCSS)
	wcss := calculateWCSS(embeddings, clusters)

	// Calinski-Harabasz index = (BCSS / (k-1)) / (WCSS / (n-k))
	k := float64(len(clusters))
	n := float64(totalPoints)

	if wcss == 0 {
		return math.Inf(1) // Perfect clustering
	}

	return (bcss / (k - 1)) / (wcss / (n - k))
}

// calculateClusterCoherence calculates average intra-cluster similarity (coherence)
func calculateClusterCoherence(embeddings []EmbeddingRecord, clusters []StoryCluster) float64 {
	embeddingMap := make(map[string][]float64)
	for _, embedding := range embeddings {
		embeddingMap[embedding.StoryID] = embedding.Embedding
	}

	totalCoherence := 0.0
	totalPairs := 0

	for _, cluster := range clusters {
		if len(cluster.Stories) < 2 {
			continue // Single-story clusters have perfect coherence but don't count
		}

		// Calculate all pairwise similarities within cluster
		clusterCoherence := 0.0
		pairCount := 0

		for i, story1 := range cluster.Stories {
			embedding1 := embeddingMap[story1.StoryID]

			for j := i + 1; j < len(cluster.Stories); j++ {
				story2 := cluster.Stories[j]
				embedding2 := embeddingMap[story2.StoryID]

				// Calculate cosine similarity
				similarity := cosineSimilarity(embedding1, embedding2)
				clusterCoherence += similarity
				pairCount++
			}
		}

		if pairCount > 0 {
			avgClusterCoherence := clusterCoherence / float64(pairCount)
			totalCoherence += avgClusterCoherence * float64(len(cluster.Stories)) // Weight by cluster size
			totalPairs += len(cluster.Stories)
		}
	}

	if totalPairs == 0 {
		return 1.0 // Perfect coherence for edge case
	}

	return totalCoherence / float64(totalPairs)
}

func assessClusteringQuality(silhouetteScore, dbIndex float64, numClusters, numStories int) string {
	var assessment []string

	// Silhouette score assessment
	if silhouetteScore > 0.7 {
		assessment = append(assessment, "Excellent cluster separation")
	} else if silhouetteScore > 0.5 {
		assessment = append(assessment, "Good cluster separation")
	} else if silhouetteScore > 0.25 {
		assessment = append(assessment, "Moderate cluster separation")
	} else if silhouetteScore > 0 {
		assessment = append(assessment, "Weak cluster separation")
	} else {
		assessment = append(assessment, "Poor cluster separation - clusters may overlap")
	}

	// Davies-Bouldin index assessment (lower is better)
	if dbIndex < 1.0 {
		assessment = append(assessment, "well-defined clusters")
	} else if dbIndex < 2.0 {
		assessment = append(assessment, "moderately defined clusters")
	} else {
		assessment = append(assessment, "poorly defined clusters")
	}

	// News-aware cluster count assessment - embrace natural story importance patterns
	avgClusterSize := float64(numStories) / float64(numClusters)
	if avgClusterSize < 1.5 {
		assessment = append(assessment, "too many micro-clusters (low grouping efficiency)")
	} else if avgClusterSize > 15 {
		assessment = append(assessment, "major story themes identified")
	} else {
		assessment = append(assessment, "balanced thematic grouping")
	}

	// Join assessments
	result := ""
	for i, a := range assessment {
		if i == 0 {
			result = a
		} else if i == len(assessment)-1 {
			result += " with " + a
		} else {
			result += ", " + a
		}
	}

	return result
}

// printClusteringQualityReport prints a comprehensive clustering quality report
func printClusteringQualityReport(result ClusteringResult) {
	summary := result.Summary

	log.Println("=====================================")
	log.Println("    CLUSTERING QUALITY REPORT")
	log.Println("=====================================")
	log.Printf("📊 Stories Processed: %d → %d clusters", summary.TotalStories, summary.TotalClusters)
	log.Printf("📈 Silhouette Score: %.3f", summary.AverageSilhouette)
	log.Printf("📉 Davies-Bouldin Index: %.3f (lower is better)", summary.DaviesBouldinIndex)
	log.Printf("🔗 Intra-cluster Distance: %.3f", summary.IntraClusterDistance)
	log.Printf("↔️  Inter-cluster Distance: %.3f", summary.InterClusterDistance)
	log.Printf("🎯 Cross-Reporter Clusters: %d/%d (%.1f%% effectiveness)",
		summary.CrossReporterClusters, summary.TotalClusters,
		float64(summary.CrossReporterClusters)/float64(summary.TotalClusters)*100)

	log.Println("\n📰 Cluster Importance (Newsworthiness):")
	for i, importance := range summary.ClusterImportance {
		log.Printf("  Cluster %d: %.3f (%.1f%% importance)", i, importance, importance*100)
	}

	log.Println("\n📈 Cluster Size Distribution:")
	for i, size := range summary.ClusterSizes {
		log.Printf("  Cluster %d: %d stories", i, size)
	}

	log.Println("\n👥 Reporter Distribution:")
	for reporter, count := range summary.ReporterDistribution {
		log.Printf("  %s: %d stories", reporter, count)
	}

	log.Printf("\n🎯 Quality Assessment: %s", summary.QualityAssessment)

	log.Println("\n📋 Detailed Cluster Composition:")
	for i, cluster := range result.Clusters {
		reporters := make(map[string]int)
		for _, story := range cluster.Stories {
			reporters[story.Reporter]++
		}

		reporterList := ""
		for reporter, count := range reporters {
			if reporterList != "" {
				reporterList += ", "
			}
			reporterList += fmt.Sprintf("%s(%d)", reporter, count)
		}

		log.Printf("  Cluster %d: %d stories [%s]", i, len(cluster.Stories), reporterList)

		// Show top 2 stories by similarity to centroid
		maxShow := 2
		if len(cluster.Stories) < maxShow {
			maxShow = len(cluster.Stories)
		}

		// Sort stories by similarity (highest first)
		for j := 0; j < len(cluster.Stories)-1; j++ {
			for k := j + 1; k < len(cluster.Stories); k++ {
				if cluster.Stories[j].Similarity < cluster.Stories[k].Similarity {
					cluster.Stories[j], cluster.Stories[k] = cluster.Stories[k], cluster.Stories[j]
				}
			}
		}

		for j := 0; j < maxShow; j++ {
			story := cluster.Stories[j]
			log.Printf("    • %s (similarity: %.3f)",
				truncateString(story.Title, 60), story.Similarity)
		}
		if len(cluster.Stories) > maxShow {
			log.Printf("    ... and %d more stories", len(cluster.Stories)-maxShow)
		}
	}

	log.Println("=====================================")
}

// truncateString truncates a string to maxLength with ellipsis
func truncateString(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength-3] + "..."
}

// DetailedQualityReport contains comprehensive per-cluster analysis
type DetailedQualityReport struct {
	Summary         ClusterSummary        `json:"summary"`
	ClusterDetails  []ClusterDetailReport `json:"cluster_details"`
	Recommendations []string              `json:"recommendations"`
	GeneratedAt     string                `json:"generated_at"`
}

// ClusterDetailReport contains detailed metrics for a single cluster
type ClusterDetailReport struct {
	ClusterID            int            `json:"cluster_id"`
	Size                 int            `json:"size"`
	Reporters            map[string]int `json:"reporters"`
	AverageSimilarity    float64        `json:"average_similarity"`
	IntraClusterDistance float64        `json:"intra_cluster_distance"`
	TopStories           []StoryBrief   `json:"top_stories"`
	IsMultiReporter      bool           `json:"is_multi_reporter"`
}

// StoryBrief contains key information about a story for reporting
type StoryBrief struct {
	StoryID    string  `json:"story_id"`
	Title      string  `json:"title"`
	Reporter   string  `json:"reporter"`
	Similarity float64 `json:"similarity"`
}

// saveClusterQualityReport saves a detailed quality analysis report
func saveClusterQualityReport(result ClusteringResult) error {
	// Generate detailed cluster reports
	var clusterDetails []ClusterDetailReport

	for i, cluster := range result.Clusters {
		reporters := make(map[string]int)
		totalSimilarity := 0.0

		for _, story := range cluster.Stories {
			reporters[story.Reporter]++
			totalSimilarity += story.Similarity
		}

		avgSimilarity := 0.0
		if len(cluster.Stories) > 0 {
			avgSimilarity = totalSimilarity / float64(len(cluster.Stories))
		}

		// Get top 3 stories by similarity
		topStories := make([]StoryBrief, 0)
		maxStories := 3
		if len(cluster.Stories) < maxStories {
			maxStories = len(cluster.Stories)
		}

		// Sort stories by similarity (create a copy to avoid modifying original)
		sortedStories := make([]ClusteredStory, len(cluster.Stories))
		copy(sortedStories, cluster.Stories)

		for j := 0; j < len(sortedStories)-1; j++ {
			for k := j + 1; k < len(sortedStories); k++ {
				if sortedStories[j].Similarity < sortedStories[k].Similarity {
					sortedStories[j], sortedStories[k] = sortedStories[k], sortedStories[j]
				}
			}
		}

		for j := 0; j < maxStories; j++ {
			story := sortedStories[j]
			topStories = append(topStories, StoryBrief{
				StoryID:    story.StoryID,
				Title:      story.Title,
				Reporter:   story.Reporter,
				Similarity: story.Similarity,
			})
		}

		clusterDetails = append(clusterDetails, ClusterDetailReport{
			ClusterID:         i,
			Size:              len(cluster.Stories),
			Reporters:         reporters,
			AverageSimilarity: avgSimilarity,
			TopStories:        topStories,
			IsMultiReporter:   len(reporters) > 1,
		})
	}

	// Generate recommendations
	recommendations := generateClusteringRecommendations(result)

	detailedReport := DetailedQualityReport{
		Summary:         result.Summary,
		ClusterDetails:  clusterDetails,
		Recommendations: recommendations,
		GeneratedAt:     time.Now().Format(time.RFC3339),
	}

	jsonData, err := json.MarshalIndent(detailedReport, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal quality report: %w", err)
	}

	if err := os.WriteFile("clusters/cluster_quality.json", jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write quality report file: %w", err)
	}

	return nil
}

// mergeSingleStoryClusters merges single-story clusters into most similar existing clusters
func mergeSingleStoryClusters(embeddings []EmbeddingRecord, clusters []StoryCluster) []StoryCluster {
	// Create embedding lookup for quick access
	embeddingMap := make(map[string][]float64)
	for _, embedding := range embeddings {
		embeddingMap[embedding.StoryID] = embedding.Embedding
	}

	var singleStoryClusters []int
	var multiStoryClusters []int

	// Identify single-story clusters
	for i, cluster := range clusters {
		if len(cluster.Stories) == 1 {
			singleStoryClusters = append(singleStoryClusters, i)
		} else {
			multiStoryClusters = append(multiStoryClusters, i)
		}
	}

	if len(singleStoryClusters) == 0 {
		log.Printf("✅ No single-story clusters to merge")
		return clusters
	}

	if len(multiStoryClusters) == 0 {
		log.Printf("⚠️  All clusters are single-story, keeping as-is")
		return clusters
	}

	log.Printf("🔍 Found %d single-story clusters to merge into %d multi-story clusters",
		len(singleStoryClusters), len(multiStoryClusters))

	// Higher threshold for news stories - require strong semantic similarity for merging
	// 0.55 cosine similarity means stories share significant thematic overlap
	similarityThreshold := 0.55 // Minimum similarity required for merging

	// Process each single-story cluster
	for _, singleIdx := range singleStoryClusters {
		singleCluster := clusters[singleIdx]
		singleStory := singleCluster.Stories[0]
		singleEmbedding := embeddingMap[singleStory.StoryID]

		// Find best cluster to merge into
		bestClusterIdx := -1
		bestSimilarity := 0.0

		for _, multiIdx := range multiStoryClusters {
			// Calculate similarity to cluster centroid
			similarity := cosineSimilarity(singleEmbedding, clusters[multiIdx].Centroid)

			if similarity > bestSimilarity {
				bestSimilarity = similarity
				bestClusterIdx = multiIdx
			}
		}

		// Merge if similarity is above threshold
		if bestClusterIdx >= 0 && bestSimilarity >= similarityThreshold {
			// Update similarity to new centroid
			singleStory.Similarity = bestSimilarity

			// Add story to best cluster
			clusters[bestClusterIdx].Stories = append(clusters[bestClusterIdx].Stories, singleStory)

			// Recalculate centroid for the target cluster
			clusters[bestClusterIdx].Centroid = calculateClusterCentroid(clusters[bestClusterIdx].Stories, embeddingMap)

			// Update similarities for all stories in the target cluster
			updateStorySimilarities(&clusters[bestClusterIdx], embeddingMap)

			log.Printf("🔗 Merged '%s' into cluster %d (similarity: %.3f)",
				truncateString(singleStory.Title, 50), bestClusterIdx, bestSimilarity)
		} else {
			log.Printf("⚠️  Could not merge '%s' - max similarity %.3f < threshold %.3f",
				truncateString(singleStory.Title, 50), bestSimilarity, similarityThreshold)
		}
	}

	// Remove empty single-story clusters and reassign IDs
	var finalClusters []StoryCluster
	for i, cluster := range clusters {
		if len(cluster.Stories) > 0 {
			cluster.ClusterID = len(finalClusters)
			finalClusters = append(finalClusters, cluster)
		} else {
			log.Printf("🗑️  Removed empty cluster %d", i)
		}
	}

	// Sort clusters by size (largest first)
	for i := 0; i < len(finalClusters)-1; i++ {
		for j := i + 1; j < len(finalClusters); j++ {
			if len(finalClusters[i].Stories) < len(finalClusters[j].Stories) {
				finalClusters[i], finalClusters[j] = finalClusters[j], finalClusters[i]
			}
		}
	}

	// Reassign cluster IDs after sorting
	for i := range finalClusters {
		finalClusters[i].ClusterID = i
	}

	return finalClusters
}

// calculateClusterCentroid calculates the centroid for a cluster of stories
func calculateClusterCentroid(stories []ClusteredStory, embeddingMap map[string][]float64) []float64 {
	if len(stories) == 0 {
		return nil
	}

	embeddingDim := len(embeddingMap[stories[0].StoryID])
	centroid := make([]float64, embeddingDim)

	// Sum all embeddings
	for _, story := range stories {
		embedding := embeddingMap[story.StoryID]
		for i, val := range embedding {
			centroid[i] += val
		}
	}

	// Average to get centroid
	for i := range centroid {
		centroid[i] /= float64(len(stories))
	}

	return centroid
}

// updateStorySimilarities updates similarity scores for all stories in a cluster
func updateStorySimilarities(cluster *StoryCluster, embeddingMap map[string][]float64) {
	for i := range cluster.Stories {
		storyEmbedding := embeddingMap[cluster.Stories[i].StoryID]
		cluster.Stories[i].Similarity = cosineSimilarity(storyEmbedding, cluster.Centroid)
	}
}

// splitLowCoherenceClusters identifies and splits clusters with poor internal coherence
// This addresses "garbage clusters" that group unrelated stories together
func splitLowCoherenceClusters(embeddings []EmbeddingRecord, clusters []StoryCluster) []StoryCluster {
	embeddingMap := make(map[string][]float64)
	for _, embedding := range embeddings {
		embeddingMap[embedding.StoryID] = embedding.Embedding
	}

	coherenceThreshold := 0.65 // Clusters must have avg similarity >= 0.65
	minClusterSize := 4        // Only split clusters with 4+ stories

	var finalClusters []StoryCluster

	for _, cluster := range clusters {
		if len(cluster.Stories) < minClusterSize {
			// Keep small clusters as-is
			finalClusters = append(finalClusters, cluster)
			continue
		}

		// Calculate average pairwise similarity (coherence)
		coherence := calculateClusterCoherenceForCluster(cluster.Stories, embeddingMap)

		if coherence >= coherenceThreshold {
			// Cluster is coherent, keep it
			finalClusters = append(finalClusters, cluster)
			log.Printf("✅ Cluster %d: coherent (%.3f >= %.3f), keeping intact", cluster.ClusterID, coherence, coherenceThreshold)
		} else {
			// Cluster has low coherence, split it using mini-hierarchical clustering
			log.Printf("⚠️  Cluster %d: low coherence (%.3f < %.3f), splitting...", cluster.ClusterID, coherence, coherenceThreshold)

			// Create embeddings subset for this cluster
			clusterEmbeddings := make([]EmbeddingRecord, len(cluster.Stories))
			for i, story := range cluster.Stories {
				clusterEmbeddings[i] = EmbeddingRecord{
					StoryID:   story.StoryID,
					VideoID:   story.VideoID,
					Title:     story.Title,
					Summary:   story.Summary,
					Reporter:  story.Reporter,
					Embedding: embeddingMap[story.StoryID],
				}
			}

			// Split into 2 sub-clusters using hierarchical clustering
			k := 2
			if len(clusterEmbeddings) >= 6 {
				k = 3 // Split into 3 if large enough
			}

			subClusters, err := performHierarchicalClustering(clusterEmbeddings, k)
			if err != nil || len(subClusters) == 0 {
				// Fallback: keep original cluster if split fails
				log.Printf("⚠️  Failed to split cluster %d, keeping as-is", cluster.ClusterID)
				finalClusters = append(finalClusters, cluster)
			} else {
				// Add split sub-clusters
				finalClusters = append(finalClusters, subClusters...)
				log.Printf("✂️  Split cluster %d into %d sub-clusters", cluster.ClusterID, len(subClusters))
			}
		}
	}

	// Sort clusters by size (largest first)
	sort.Slice(finalClusters, func(i, j int) bool {
		return len(finalClusters[i].Stories) > len(finalClusters[j].Stories)
	})

	// Reassign cluster IDs after sorting
	for i := range finalClusters {
		finalClusters[i].ClusterID = i
	}

	return finalClusters
}

// calculateClusterCoherenceForCluster calculates coherence for a single cluster
func calculateClusterCoherenceForCluster(stories []ClusteredStory, embeddingMap map[string][]float64) float64 {
	if len(stories) < 2 {
		return 1.0 // Perfect coherence for single-story clusters
	}

	totalSimilarity := 0.0
	pairCount := 0

	for i := 0; i < len(stories); i++ {
		embedding1 := embeddingMap[stories[i].StoryID]
		for j := i + 1; j < len(stories); j++ {
			embedding2 := embeddingMap[stories[j].StoryID]
			similarity := cosineSimilarity(embedding1, embedding2)
			totalSimilarity += similarity
			pairCount++
		}
	}

	if pairCount == 0 {
		return 1.0
	}

	return totalSimilarity / float64(pairCount)
}

// generateClusteringRecommendations provides actionable recommendations based on clustering results
func generateClusteringRecommendations(result ClusteringResult) []string {
	var recommendations []string
	summary := result.Summary

	// Silhouette score recommendations
	if summary.AverageSilhouette < 0.2 {
		recommendations = append(recommendations, "Consider reducing the number of clusters (k) as silhouette score is very low")
		recommendations = append(recommendations, "Try different clustering algorithms (hierarchical, DBSCAN) for better results")
	} else if summary.AverageSilhouette < 0.5 {
		recommendations = append(recommendations, "Moderate clustering quality - consider fine-tuning k value")
	}

	// Davies-Bouldin index recommendations
	if summary.DaviesBouldinIndex > 2.0 {
		recommendations = append(recommendations, "High Davies-Bouldin index suggests poorly separated clusters")
		recommendations = append(recommendations, "Consider using different distance metrics or pre-processing techniques")
	}

	// Cross-reporter effectiveness
	crossReporterRatio := float64(summary.CrossReporterClusters) / float64(summary.TotalClusters)
	if crossReporterRatio < 0.3 {
		recommendations = append(recommendations, "Low cross-reporter clustering effectiveness - stories may be too dissimilar between reporters")
		recommendations = append(recommendations, "Consider adjusting embedding parameters or using topic-specific clustering")
	} else if crossReporterRatio > 0.8 {
		recommendations = append(recommendations, "High cross-reporter clustering - good semantic grouping across sources")
	}

	// News-aware cluster size assessment - embrace natural importance patterns
	avgClusterSize := float64(summary.TotalStories) / float64(summary.TotalClusters)
	if avgClusterSize < 1.8 {
		recommendations = append(recommendations, "Too many micro-clusters - consider reducing k for better thematic grouping")
	} else if avgClusterSize > 12 {
		recommendations = append(recommendations, "Major story themes well-identified - large clusters indicate important topics")
	}

	// Distance ratio analysis
	if summary.InterClusterDistance > 0 && summary.IntraClusterDistance/summary.InterClusterDistance > 0.8 {
		recommendations = append(recommendations, "Clusters may be too loose - intra-cluster distance is high relative to inter-cluster distance")
	}

	if len(recommendations) == 0 {
		recommendations = append(recommendations, "Clustering quality looks good overall")
		recommendations = append(recommendations, "Monitor cross-reporter effectiveness in future runs")
	}

	return recommendations
}