package chd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Result describes a written CHD.
type Result struct {
	InputSize  int64 // bytes of the source files
	OutputSize int64
	Tracks     int // CD tracks; 0 for a DVD
}

// ConvertCue writes the disc of a CUE sheet as a CD CHD, as chdman createcd
// does (codecs cdlz, cdzl and cdfl).
func ConvertCue(ctx context.Context, cuePath, outPath string, opt Options) (*Result, error) {
	tracks, err := ParseCue(cuePath)
	if err != nil {
		return nil, err
	}
	return convertTracks(ctx, tracks, outPath, opt)
}

// ConvertISOAsCD writes a 2048-byte sector image as a CD CHD with one
// Mode 1 track, as chdman createcd does with an .iso: for CD games kept
// as ISO (PS1, PS2 CD games and others).
func ConvertISOAsCD(ctx context.Context, isoPath, outPath string, opt Options) (*Result, error) {
	st, err := os.Stat(isoPath)
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 || st.Size()%DVDSectorSize != 0 {
		return nil, fmt.Errorf("%s: size is not a multiple of %d bytes: not a 2048-byte sector image", st.Name(), DVDSectorSize)
	}
	t := &Track{Number: 1, Type: "MODE1", DataSize: DVDSectorSize, File: isoPath, Frames: int(st.Size() / DVDSectorSize)}
	return convertTracks(ctx, []*Track{t}, outPath, opt)
}

func convertTracks(ctx context.Context, tracks []*Track, outPath string, opt Options) (*Result, error) {
	res := &Result{Tracks: len(tracks)}
	l := layout{hunkBytes: FramesPerHunk * FrameSize, unitBytes: FrameSize, codecs: []uint32{codecCDLZMA, codecCDZlib, codecCDFLAC}}
	var frames int64
	files := map[string]bool{}
	for i, t := range tracks {
		frames += int64(t.Frames + t.extraFrames())
		md := t.metadata()
		// chdman numbers the tracks from 1 in their order.
		md = strings.Replace(md, fmt.Sprintf("TRACK:%d ", t.Number), fmt.Sprintf("TRACK:%d ", i+1), 1)
		tag := tagCDTrack
		if t.gdrom {
			tag = tagGDTrack
		}
		l.meta = append(l.meta, metadata{tag, metaChecksum, append([]byte(md), 0)})
		if !files[t.File] {
			files[t.File] = true
			if st, err := os.Stat(t.File); err == nil {
				res.InputSize += st.Size()
			}
		}
	}
	l.logicalBytes = frames * FrameSize

	src := &cdSource{tracks: tracks}
	defer src.close()
	if err := writeCHD(ctx, outPath, l, src.fill, opt); err != nil {
		return nil, err
	}
	if st, err := os.Stat(outPath); err == nil {
		res.OutputSize = st.Size()
	}
	return res, nil
}

// cdSource produces the CD frames of the tracks in order.
type cdSource struct {
	tracks []*Track
	track  int // current track
	frame  int // next frame within it, counting padding
	f      *os.File
	r      *bufio.Reader
	name   string
}

func (s *cdSource) close() {
	if s.f != nil {
		s.f.Close()
	}
}

func (s *cdSource) open(t *Track) error {
	if s.name != t.File {
		s.close()
		f, err := os.Open(t.File)
		if err != nil {
			return err
		}
		s.f, s.name = f, t.File
	}
	if _, err := s.f.Seek(t.Offset, io.SeekStart); err != nil {
		return err
	}
	s.r = bufio.NewReaderSize(s.f, 1<<20)
	return nil
}

// fill fills a hunk with the next frames; past the last track it stays zero.
func (s *cdSource) fill(hunk []byte) error {
	for off := 0; off < len(hunk); off += FrameSize {
		frame := hunk[off : off+FrameSize]
		for s.track < len(s.tracks) && s.frame >= s.tracks[s.track].Frames+s.tracks[s.track].extraFrames() {
			s.track, s.frame = s.track+1, 0
		}
		if s.track >= len(s.tracks) {
			return nil
		}
		t := s.tracks[s.track]
		if s.frame == 0 {
			if err := s.open(t); err != nil {
				return err
			}
		}
		// A track's frames: its own data, then on a GD-ROM empty padding,
		// then frames from the start of the next track's file.
		data := t.Frames - t.Pad - t.Split
		if s.frame == data+t.Pad && t.Split > 0 && s.track+1 < len(s.tracks) {
			next := *s.tracks[s.track+1]
			next.Offset = 0
			if err := s.open(&next); err != nil {
				return err
			}
		}
		if s.frame < data || s.frame >= data+t.Pad && s.frame < t.Frames {
			if _, err := io.ReadFull(s.r, frame[:t.DataSize]); err != nil {
				if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
					return fmt.Errorf("%s: file ends before track %d does", s.name, t.Number)
				}
				return err
			}
			if t.swap {
				for i := 0; i < SectorSize; i += 2 {
					frame[i], frame[i+1] = frame[i+1], frame[i]
				}
			}
		}
		s.frame++
	}
	return nil
}

// ConvertISO writes a 2048-byte sector image (PS2 DVD, PSP UMD and other
// DVD images) as a DVD CHD, as chdman createdvd does (codecs lzma and zlib).
func ConvertISO(ctx context.Context, isoPath, outPath string, opt Options) (*Result, error) {
	f, err := os.Open(isoPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 || st.Size()%DVDSectorSize != 0 {
		return nil, fmt.Errorf("%s: size is not a multiple of %d bytes: not a 2048-byte sector image", st.Name(), DVDSectorSize)
	}
	l := layout{
		hunkBytes:    dvdHunkBytes,
		unitBytes:    DVDSectorSize,
		logicalBytes: st.Size(),
		codecs:       []uint32{codecLZMA, codecZlib},
		meta:         []metadata{{tagDVD, metaChecksum, []byte{0}}},
	}
	r := bufio.NewReaderSize(f, 1<<20)
	fill := func(hunk []byte) error {
		_, err := io.ReadFull(r, hunk)
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			return nil // last hunk: the rest stays zero
		}
		return err
	}
	if err := writeCHD(ctx, outPath, l, fill, opt); err != nil {
		return nil, err
	}
	res := &Result{InputSize: st.Size()}
	if st, err := os.Stat(outPath); err == nil {
		res.OutputSize = st.Size()
	}
	return res, nil
}
