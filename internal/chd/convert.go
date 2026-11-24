package chd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Result describes a written CHD.
type Result struct {
	InputSize  int64 // bytes of the source files
	OutputSize int64
	Tracks     int // CD tracks; 0 for a DVD
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
			res.InputSize += ImageFileSize(t.File)
		}
	}
	l.logicalBytes = frames * FrameSize

	src := &cdSource{tracks: tracks}
	defer src.close()
	if err := writeCHD(ctx, outPath, l, src.fill, opt); err != nil {
		return nil, err
	}
	if err := src.checkECM(); err != nil {
		os.Remove(outPath)
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
	images map[string]*Image
	r      *bufio.Reader
	name   string
}

func (s *cdSource) close() {
	for _, img := range s.images {
		img.Close()
	}
}

// checkECM tells whether the ECM files read match their checksums.
func (s *cdSource) checkECM() error {
	for name, img := range s.images {
		if err := img.CheckECM(); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(name), err)
		}
	}
	return nil
}

func (s *cdSource) open(t *Track, offset int64) error {
	if s.images == nil {
		s.images = map[string]*Image{}
	}
	img := s.images[t.File]
	if img == nil {
		var err error
		if img, err = OpenImage(t.File); err != nil {
			return err
		}
		s.images[t.File] = img
	}
	s.name = t.File
	s.r = bufio.NewReaderSize(io.NewSectionReader(img, offset, img.Size()-offset), 1<<20)
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
		if s.frame == t.lead {
			if err := s.open(t, t.Offset); err != nil {
				return err
			}
		}
		// A track's frames: a gap the image does not store, its own data,
		// then on a GD-ROM empty padding, then frames from the start of the
		// next track's file.
		fr := s.frame - t.lead
		data := t.Frames - t.lead - t.Pad - t.Split
		if fr == data+t.Pad && t.Split > 0 && s.track+1 < len(s.tracks) {
			if err := s.open(s.tracks[s.track+1], 0); err != nil {
				return err
			}
		}
		if fr >= 0 && (fr < data || fr >= data+t.Pad && s.frame < t.Frames) {
			if _, err := io.ReadFull(s.r, frame[:t.DataSize]); err != nil {
				if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
					return fmt.Errorf("%s: file ends before track %d does", filepath.Base(s.name), t.Number)
				}
				return err
			}
			if skip := t.SectorStride() - t.DataSize; skip > 0 {
				if _, err := s.r.Discard(skip); err != nil {
					return fmt.Errorf("%s: file ends before track %d does", filepath.Base(s.name), t.Number)
				}
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
