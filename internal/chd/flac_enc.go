package chd

import (
	"math"
	"math/bits"
	"slices"
)

// FLAC encoder for the CHD codecs: frames with constant, verbatim, fixed
// predictor and LPC subframes. For each frame it plans the four channels
// (left, right, mid, side) and keeps the cheapest stereo coding, as
// libFLAC does at its higher levels. The LPC coefficients come from the
// autocorrelation of the windowed signal (Levinson-Durbin), quantized as
// FLAC stores them; the orders that the prediction error suggests are
// tried exactly, and the smallest residual wins.

const (
	flacMaxLPCOrder = 12
	flacMaxPart     = 8 // highest Rice partition order tried
)

// flacLPCOrders is how many of the best estimated LPC orders are tried per
// window (0 turns LPC off).
var flacLPCOrders = 2

// flacPrecisions are the coefficient precisions (bits) tried.
var flacPrecisions = []int{12, 15}

// flacWindows are the apodization windows (Tukey with these ratios).
var flacWindows = []float64{0.5, 0.1}

// subframe kinds.
const (
	subConstant = iota
	subVerbatim
	subFixed
	subLPC
)

// subPlan is how a channel of a frame is coded.
type subPlan struct {
	kind  int
	order int
	prec  int
	shift int
	coefs [flacMaxLPCOrder]int32
	part  int
	bits  uint64 // estimated size of the subframe
	res   []int32
}

type flacEncoder struct {
	w       bitWriter
	scratch [4][]int32
	plans   [4]subPlan
	cand    subPlan
	win     map[winKey][]float64
	wx      []float64
	sums    []uint64
}

type winKey struct {
	n int
	p float64
}

// encode appends the frames for the samples (all frames blockSize long)
// to a new buffer and returns it.
func (e *flacEncoder) encode(left, right []int32, blockSize int) []byte {
	e.w = bitWriter{buf: e.w.buf[:0]}
	for f, i := 0, 0; i < len(left); f, i = f+1, i+blockSize {
		end := min(i+blockSize, len(left))
		e.frame(left[i:end], right[i:end], f)
	}
	return append([]byte(nil), e.w.bytes()...)
}

func (e *flacEncoder) frame(left, right []int32, number int) {
	n := len(left)
	for k := range e.scratch {
		if cap(e.scratch[k]) < n {
			e.scratch[k] = make([]int32, n)
		}
		e.scratch[k] = e.scratch[k][:n]
	}
	mid, side := e.scratch[0], e.scratch[1]
	for i := range n {
		mid[i] = (left[i] + right[i]) >> 1
		side[i] = left[i] - right[i]
	}
	chans := [4][]int32{left, right, mid, side}
	bps := [4]uint{16, 16, 16, 17}
	for c := range 4 {
		e.plan(&e.plans[c], chans[c], bps[c])
	}
	L, R, M, S := &e.plans[0], &e.plans[1], &e.plans[2], &e.plans[3]
	type choice struct {
		code   uint64
		a, b   int
		ab, bb uint
		cost   uint64
	}
	best := choice{1, 0, 1, 16, 16, L.bits + R.bits}
	for _, c := range []choice{{8, 0, 3, 16, 17, L.bits + S.bits}, {9, 3, 1, 17, 16, S.bits + R.bits}, {10, 2, 3, 16, 17, M.bits + S.bits}} {
		if c.cost < best.cost {
			best = c
		}
	}

	start := len(e.w.bytes())
	w := &e.w
	w.write(0xfff8, 16) // sync, fixed block size
	w.write(7, 4)       // block size: 16 bits at the end of the header
	w.write(9, 4)       // 44.1 kHz
	w.write(best.code, 4)
	w.write(4, 3) // 16 bits per sample
	w.write(0, 1)
	writeUTF8(w, uint64(number))
	w.write(uint64(n-1), 16)
	w.write(uint64(flacCRC8(w.bytes()[start:])), 8)
	e.write(&e.plans[best.a], chans[best.a], best.ab)
	e.write(&e.plans[best.b], chans[best.b], best.bb)
	w.bytes() // pad to a byte
	w.write(uint64(flacCRC16(w.bytes()[start:])), 16)
}

func writeUTF8(w *bitWriter, v uint64) {
	if v < 0x80 {
		w.write(v, 8)
		return
	}
	n := (bits.Len64(v) - 2) / 5 // continuation bytes
	w.write(uint64(0xff00>>(n+1))&0xff|v>>(6*n), 8)
	for i := n - 1; i >= 0; i-- {
		w.write(0x80|(v>>(6*i))&0x3f, 8)
	}
}

func abs64(v int32) uint64 {
	if v < 0 {
		return uint64(-int64(v))
	}
	return uint64(v)
}

// riceParam returns the Rice parameter for n residuals with absolute sum s
// and the estimated bits.
func riceParam(n int, s uint64) (uint64, uint) {
	if n == 0 {
		return 0, 0
	}
	k := uint(0)
	if mean := 2 * s / uint64(n); mean > 0 {
		k = uint(bits.Len64(mean)) - 1
	}
	k = min(k, 14)
	return uint64(n)*uint64(k+1) + 2*s>>k, k
}

// residualBits picks the Rice partition order for a residual of a block of
// n samples whose first order samples are warm-up, and returns it with the
// estimated bits of the residual section.
func (e *flacEncoder) residualBits(res []int32, n, order int) (uint64, int) {
	maxPart := 0
	for maxPart < flacMaxPart && n%(2<<maxPart) == 0 && n>>(maxPart+1) > order {
		maxPart++
	}
	if cap(e.sums) < 1<<maxPart {
		e.sums = make([]uint64, 1<<flacMaxPart)
	}
	sums := e.sums[:1<<maxPart]
	clear(sums)
	plen := n >> maxPart
	for i, v := range res {
		sums[(i+order)/plen] += abs64(v)
	}
	bestBits, bestPart := uint64(math.MaxUint64), 0
	for p := maxPart; p >= 0; p-- {
		total := uint64(6) // coding method and partition order
		cnt := n >> p
		for j := range 1 << p {
			c := cnt
			if j == 0 {
				c -= order
			}
			b, _ := riceParam(c, sums[j])
			total += b + 4
		}
		if total < bestBits {
			bestBits, bestPart = total, p
		}
		if p > 0 { // merge pairs for the next lower order
			for j := range 1 << (p - 1) {
				sums[j] = sums[2*j] + sums[2*j+1]
			}
		}
	}
	return bestBits, bestPart
}

// plan chooses the coding of one channel.
func (e *flacEncoder) plan(p *subPlan, x []int32, bps uint) {
	n := len(x)
	p.res = p.res[:0]
	if !slices.ContainsFunc(x[1:], func(v int32) bool { return v != x[0] }) {
		p.kind, p.bits = subConstant, 8+uint64(bps)
		return
	}
	p.kind, p.bits = subVerbatim, 8+uint64(n)*uint64(bps)
	if n <= 4 {
		return
	}

	// Fixed predictors: the order with the smallest absolute residual.
	var sum [5]uint64
	for i := 4; i < n; i++ {
		e0 := x[i]
		e1 := e0 - x[i-1]
		e2 := e1 - (x[i-1] - x[i-2])
		e3 := e2 - (x[i-1] - 2*x[i-2] + x[i-3])
		e4 := e3 - (x[i-1] - 3*x[i-2] + 3*x[i-3] - x[i-4])
		sum[0] += abs64(e0)
		sum[1] += abs64(e1)
		sum[2] += abs64(e2)
		sum[3] += abs64(e3)
		sum[4] += abs64(e4)
	}
	order := 0
	for o := 1; o < 5; o++ {
		if sum[o] < sum[order] {
			order = o
		}
	}
	c := &e.cand
	c.kind, c.order = subFixed, order
	c.res = fixedResidual(x, order, c.res)
	rb, part := e.residualBits(c.res, n, order)
	c.part, c.bits = part, 8+uint64(order)*uint64(bps)+rb
	if c.bits < p.bits {
		*p, *c = *c, *p
	}

	if flacLPCOrders > 0 && n > flacMaxLPCOrder*2 {
		e.planLPC(p, x, bps)
	}
}

// planLPC tries LPC predictors and keeps one in p if it is smaller.
func (e *flacEncoder) planLPC(p *subPlan, x []int32, bps uint) {
	n := len(x)
	maxOrder := flacMaxLPCOrder
	if cap(e.wx) < n {
		e.wx = make([]float64, n)
	}
	wx := e.wx[:n]
	for _, ratio := range flacWindows {
		win := e.window(n, ratio)
		for i, v := range x {
			wx[i] = float64(v) * win[i]
		}
		var ac [flacMaxLPCOrder + 1]float64
		for lag := 0; lag <= maxOrder; lag++ {
			s := 0.0
			for i := lag; i < n; i++ {
				s += wx[i] * wx[i-lag]
			}
			ac[lag] = s
		}
		if ac[0] == 0 {
			continue
		}
		var lp [flacMaxLPCOrder][flacMaxLPCOrder]float64
		var errs [flacMaxLPCOrder]float64
		orders := levinson(ac[:maxOrder+1], &lp, &errs)
		if orders == 0 {
			continue
		}
		// Rank the orders by expected size.
		type est struct {
			order int
			bits  float64
		}
		var ests []est
		scale := 0.5 / float64(n)
		for o := 1; o <= orders; o++ {
			bpsRes := 0.0
			if v := errs[o-1] * scale; v > 0 {
				bpsRes = max(0.5*math.Log2(v), 0)
			}
			ests = append(ests, est{o, bpsRes*float64(n-o) + float64(o)*(float64(bps)+12)})
		}
		slices.SortFunc(ests, func(a, b est) int {
			switch {
			case a.bits < b.bits:
				return -1
			case a.bits > b.bits:
				return 1
			}
			return 0
		})
		for _, es := range ests[:min(flacLPCOrders, len(ests))] {
			for _, prec := range flacPrecisions {
				c := &e.cand
				if !quantize(lp[es.order-1][:es.order], prec, c) {
					continue
				}
				c.kind, c.order, c.prec = subLPC, es.order, prec
				var ok bool
				c.res, ok = lpcResidual(x, c.coefs[:c.order], c.shift, c.res)
				if !ok {
					continue
				}
				rb, part := e.residualBits(c.res, n, c.order)
				c.part = part
				c.bits = 8 + uint64(c.order)*uint64(bps) + 4 + 5 + uint64(c.order*prec) + rb
				if c.bits < p.bits {
					*p, *c = *c, *p
				}
			}
		}
	}
}

// window returns a Tukey window of n samples with the given ratio.
func (e *flacEncoder) window(n int, p float64) []float64 {
	k := winKey{n, p}
	if w, ok := e.win[k]; ok {
		return w
	}
	w := make([]float64, n)
	np := int(p / 2 * float64(n))
	for i := range n {
		w[i] = 1
		switch {
		case np > 0 && i < np:
			w[i] = 0.5 - 0.5*math.Cos(math.Pi*float64(i)/float64(np))
		case np > 0 && i >= n-np:
			w[i] = 0.5 - 0.5*math.Cos(math.Pi*float64(n-1-i)/float64(np))
		}
	}
	if e.win == nil {
		e.win = map[winKey][]float64{}
	}
	e.win[k] = w
	return w
}

// levinson computes the LPC coefficients of every order from the
// autocorrelation ac (Levinson-Durbin): lp[o-1] holds those of order o and
// errs[o-1] the prediction error. It returns the highest usable order.
func levinson(ac []float64, lp *[flacMaxLPCOrder][flacMaxLPCOrder]float64, errs *[flacMaxLPCOrder]float64) int {
	var a [flacMaxLPCOrder]float64
	errv := ac[0]
	for i := 0; i < len(ac)-1; i++ {
		r := -ac[i+1]
		for j := range i {
			r -= a[j] * ac[i-j]
		}
		if errv <= 0 {
			return i
		}
		r /= errv
		a[i] = r
		for j := range i / 2 {
			t := a[j]
			a[j] += r * a[i-1-j]
			a[i-1-j] += r * t
		}
		if i%2 == 1 {
			a[i/2] += a[i/2] * r
		}
		errv *= 1 - r*r
		for j := 0; j <= i; j++ {
			lp[i][j] = -a[j]
		}
		errs[i] = errv
	}
	return len(ac) - 1
}

// quantize stores lp as FLAC coefficients of prec bits in c (coefs, shift).
func quantize(lp []float64, prec int, c *subPlan) bool {
	cmax := 0.0
	for _, v := range lp {
		cmax = max(cmax, math.Abs(v))
	}
	if cmax <= 0 || math.IsNaN(cmax) || math.IsInf(cmax, 0) {
		return false
	}
	p := prec - 1
	qmax, qmin := int32(1)<<p-1, -int32(1)<<p
	_, log2cmax := math.Frexp(cmax)
	log2cmax--
	shift := p - log2cmax - 1
	if shift < 0 {
		return false
	}
	shift = min(shift, 15)
	errv := 0.0
	for i, v := range lp {
		errv += v * float64(int64(1)<<shift)
		q := math.Round(errv)
		qi := int32(max(min(q, float64(qmax)), float64(qmin)))
		errv -= float64(qi)
		c.coefs[i] = qi
	}
	c.shift = shift
	return true
}

// lpcResidual computes the residual of an LPC predictor; false if a value
// does not fit the residual coding.
func lpcResidual(x []int32, coefs []int32, shift int, res []int32) ([]int32, bool) {
	res = res[:0]
	order := len(coefs)
	for i := order; i < len(x); i++ {
		var s int64
		for j, c := range coefs {
			s += int64(c) * int64(x[i-1-j])
		}
		v := int64(x[i]) - s>>shift
		if v > math.MaxInt32/2 || v < math.MinInt32/2 {
			return res, false
		}
		res = append(res, int32(v))
	}
	return res, true
}

// fixedResidual returns the residual of the fixed predictor of an order.
func fixedResidual(x []int32, order int, res []int32) []int32 {
	res = res[:0]
	for i := order; i < len(x); i++ {
		var v int32
		switch order {
		case 0:
			v = x[i]
		case 1:
			v = x[i] - x[i-1]
		case 2:
			v = x[i] - 2*x[i-1] + x[i-2]
		case 3:
			v = x[i] - 3*x[i-1] + 3*x[i-2] - x[i-3]
		case 4:
			v = x[i] - 4*x[i-1] + 6*x[i-2] - 4*x[i-3] + x[i-4]
		}
		res = append(res, v)
	}
	return res
}

// write writes the subframe planned in p for the samples x.
func (e *flacEncoder) write(p *subPlan, x []int32, bps uint) {
	w := &e.w
	mask := uint64(1)<<bps - 1
	switch p.kind {
	case subConstant:
		w.write(0, 8) // pad, type 0, no wasted bits
		w.write(uint64(x[0])&mask, bps)
		return
	case subVerbatim:
		w.write(1<<1, 8)
		for _, v := range x {
			w.write(uint64(v)&mask, bps)
		}
		return
	case subFixed:
		w.write(uint64(8+p.order)<<1, 8)
		for _, v := range x[:p.order] {
			w.write(uint64(v)&mask, bps)
		}
	case subLPC:
		w.write(uint64(32+p.order-1)<<1, 8)
		for _, v := range x[:p.order] {
			w.write(uint64(v)&mask, bps)
		}
		w.write(uint64(p.prec-1), 4)
		w.write(uint64(p.shift)&0x1f, 5)
		for _, c := range p.coefs[:p.order] {
			w.write(uint64(c)&(1<<p.prec-1), uint(p.prec))
		}
	}
	w.write(0, 2) // Rice, 4-bit parameters
	w.write(uint64(p.part), 4)
	n := len(x)
	plen := n >> p.part
	i := 0
	for j := range 1 << p.part {
		end := (j+1)*plen - p.order
		k := bestRice(p.res[i:end])
		w.write(uint64(k), 4)
		for ; i < end; i++ {
			v := p.res[i]
			u := uint64(uint32(v<<1) ^ uint32(v>>31))
			q := u >> k
			for q >= 32 {
				w.write(0, 32)
				q -= 32
			}
			w.write(1, uint(q)+1)
			w.write(u&(1<<k-1), k)
		}
	}
}

// bestRice returns the Rice parameter with the fewest bits for a partition,
// trying the estimate and its neighbours exactly.
func bestRice(res []int32) uint {
	var s uint64
	for _, v := range res {
		s += abs64(v)
	}
	_, k0 := riceParam(len(res), s)
	best, bestBits := k0, uint64(math.MaxUint64)
	for k := max(int(k0)-1, 0); k <= min(int(k0)+1, 14); k++ {
		b := uint64(len(res)) * uint64(k+1)
		for _, v := range res {
			b += uint64(uint32(v<<1)^uint32(v>>31)) >> k
		}
		if b < bestBits {
			best, bestBits = uint(k), b
		}
	}
	return best
}
