package imagestore

import "math"

// DeepSeek-VL2 image-token constants (processor_config.json):
//
//	image_size = 384, patch_size = 14, downsample_ratio = 2
//
// The processor tokenizes an image as a global view plus optional local tiles:
//
//	base   = ceil((384 / 14) / 2) = 14
//	global = base * (base + 1) + 1          // 211, +1 separates global/local
//	local  = (nh * base) * (nw * base + 1)  // nw/nh = best-resolution tiles
//	total  = global + local
//
// When more than two images are present in one request DeepSeek-VL2 disables
// cropping, so each image is exactly one 384x384 tile (421 tokens).
const (
	dsImageSize    = 384
	dsPatchSize    = 14
	dsDownsample   = 2
	dsBase         = (dsImageSize/dsPatchSize + dsDownsample - 1) / dsDownsample // 14
	dsGlobalTokens = dsBase*(dsBase+1) + 1                                       // 211
	dsNoCropTokens = dsGlobalTokens + dsBase*(dsBase+1)                          // 421
)

// dsCandidateResolutions is copied verbatim from the DeepSeek-VL2
// processor_config.json candidate_resolutions list.
var dsCandidateResolutions = [][2]int{
	{384, 384},
	{384, 768},
	{768, 384},
	{384, 1152},
	{1152, 384},
	{384, 1536},
	{1536, 384},
	{768, 768},
	{384, 1920},
	{1920, 384},
	{384, 2304},
	{2304, 384},
	{768, 1152},
	{1152, 768},
	{384, 2688},
	{2688, 384},
	{384, 3072},
	{3072, 384},
	{768, 1536},
	{1536, 768},
	{384, 3456},
	{3456, 384},
	{1152, 1152},
}

// selectBestResolution mirrors DeepSeek-VL2's select_best_resolution:
// pick the candidate with the largest effective (downscaled) resolution,
// breaking ties by the smallest wasted area.
func selectBestResolution(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return dsImageSize, dsImageSize
	}
	original := float64(width * height)
	bestW, bestH := dsImageSize, dsImageSize
	bestEffective := -1.0
	bestWaste := math.MaxFloat64
	for _, cand := range dsCandidateResolutions {
		cw, ch := cand[0], cand[1]
		scale := math.Min(float64(cw)/float64(width), float64(ch)/float64(height))
		dw := float64(width) * scale
		dh := float64(height) * scale
		effective := math.Min(dw*dh, original)
		waste := float64(cw*ch) - effective
		if effective > bestEffective || (effective == bestEffective && waste < bestWaste) {
			bestEffective = effective
			bestWaste = waste
			bestW = cw
			bestH = ch
		}
	}
	return bestW, bestH
}

// DeepSeekImageTokens estimates the image token count using the DeepSeek-VL2
// processor formula. cropping should be true when the request contains at most
// two images (the processor enables cropping in that case).
func DeepSeekImageTokens(width, height int, cropping bool) int {
	if width <= 0 || height <= 0 {
		return 0
	}
	nw, nh := 1, 1
	if cropping {
		bw, bh := selectBestResolution(width, height)
		nw, nh = bw/dsImageSize, bh/dsImageSize
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
	}
	return dsGlobalTokens + (nh*dsBase)*(nw*dsBase+1)
}
