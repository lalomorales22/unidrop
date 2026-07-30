package main

func drawStateIcon(pixels []byte, size int, state iconState) {
	center := float64(size-1) / 2
	radius := float64(size) * 0.46
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-center, float64(y)-center
			if dx*dx+dy*dy > radius*radius {
				continue
			}
			red, green, blue := byte(75), byte(92), byte(255)
			switch state {
			case iconOffline:
				red, green, blue = 105, 110, 124
			case iconNearby:
				red, green, blue = 50, 184, 142
			case iconAttention:
				red, green, blue = 244, 91, 112
			}
			shade := byte((y * 25) / size)
			setBGRAPixel(pixels, size, x, y, red, clampByte(int(green)+int(shade)), clampByte(int(blue)+int(shade)/2), 255)
		}
	}
	stroke := maxInt(1, size/16)
	drawArrow(pixels, size, size*7/20, size*5/20, size*14/20, true, stroke)
	drawArrow(pixels, size, size*13/20, size*15/20, size*6/20, false, stroke)
	if state == iconAttention {
		radius := maxInt(1, size/9)
		centerX, centerY := size-radius-1, size-radius-1
		for y := size - radius*2 - 1; y < size-1; y++ {
			for x := size - radius*2 - 1; x < size-1; x++ {
				dx, dy := x-centerX, y-centerY
				if dx*dx+dy*dy <= radius*radius {
					setBGRAPixel(pixels, size, x, y, 255, 218, 84, 255)
				}
			}
		}
	}
}

func drawArrow(pixels []byte, size, x, start, end int, down bool, stroke int) {
	low, high := start, end
	if low > high {
		low, high = high, low
	}
	for y := low; y <= high; y++ {
		for offset := -stroke; offset <= stroke; offset++ {
			setBGRAPixel(pixels, size, x+offset, y, 255, 255, 255, 255)
		}
	}
	tipY, direction := end, -1
	if !down {
		direction = 1
	}
	wing := maxInt(2, size/7)
	for step := 0; step <= wing; step++ {
		for offset := -stroke; offset <= stroke; offset++ {
			setBGRAPixel(pixels, size, x-step, tipY+direction*step+offset, 255, 255, 255, 255)
			setBGRAPixel(pixels, size, x+step, tipY+direction*step+offset, 255, 255, 255, 255)
		}
	}
}

func setBGRAPixel(pixels []byte, size, x, y int, red, green, blue, alpha byte) {
	if x < 0 || y < 0 || x >= size || y >= size {
		return
	}
	offset := (y*size + x) * 4
	pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3] = blue, green, red, alpha
}

func clampByte(value int) byte {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return byte(value)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
