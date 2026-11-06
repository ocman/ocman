package protocol

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var svgElements = strings.Fields("svg g path circle rect ellipse line polyline polygon title desc")
var svgAttributes = strings.Fields("viewBox width height x y x1 x2 y1 y2 cx cy r rx ry d points fill stroke stroke-width stroke-linecap stroke-linejoin stroke-miterlimit fill-rule clip-rule opacity fill-opacity stroke-opacity transform")
var svgNumberList = regexp.MustCompile(`^[0-9eE+., \t\r\n-]+$`)
var svgPathData = regexp.MustCompile(`^[MmZzLlHhVvCcSsQqTtAa0-9eE+., \t\r\n-]+$`)
var svgColor = regexp.MustCompile(`^(none|currentColor|black|white|#[0-9a-fA-F]{3}|#[0-9a-fA-F]{6})$`)
var svgTransform = regexp.MustCompile(`^(matrix|translate|scale|rotate|skewX|skewY)\([0-9eE+., \t\r\n-]+\)([ \t]+(matrix|translate|scale|rotate|skewX|skewY)\([0-9eE+., \t\r\n-]+\))*$`)

// ValidateIconSVG accepts inert monochrome geometry only. No markup is ever
// inserted into the host DOM, but reject active/reference content at the owner
// and again at the HTTP boundary as defense in depth.
func ValidateIconSVG(data []byte) error {
	if len(data) == 0 || len(data) > MaxIconBytes || !utf8.Valid(data) {
		return ErrInvalidMessage
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	depth, roots, elements := 0, 0, 0
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			if roots != 1 || depth != 0 {
				return ErrInvalidMessage
			}
			return nil
		}
		if err != nil {
			return ErrInvalidMessage
		}
		switch t := token.(type) {
		case xml.StartElement:
			elements++
			if depth >= 32 || elements > 2048 || !slices.Contains(svgElements, t.Name.Local) || t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg" {
				return ErrInvalidMessage
			}
			if depth == 0 {
				roots++
				if roots != 1 || t.Name.Local != "svg" {
					return ErrInvalidMessage
				}
			} else if t.Name.Local == "svg" {
				return ErrInvalidMessage
			}
			seen := map[string]bool{}
			for _, a := range t.Attr {
				if seen[a.Name.Local] {
					return ErrInvalidMessage
				}
				seen[a.Name.Local] = true
				if a.Name.Space == "" && a.Name.Local == "xmlns" && a.Value == "http://www.w3.org/2000/svg" {
					continue
				}
				if a.Name.Space != "" || !validSVGAttribute(a.Name.Local, a.Value) {
					return ErrInvalidMessage
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return ErrInvalidMessage
			}
		case xml.Comment:
			// Comments contain no rendered content.
		default:
			// Includes processing instructions, DTDs and entity declarations.
			return ErrInvalidMessage
		}
	}
}

func validSVGAttribute(name, value string) bool {
	if !slices.Contains(svgAttributes, name) || len(value) > MaxIconBytes || value == "" {
		return false
	}
	switch name {
	case "fill", "stroke":
		return svgColor.MatchString(value)
	case "stroke-linecap":
		return value == "butt" || value == "round" || value == "square"
	case "stroke-linejoin":
		return value == "miter" || value == "round" || value == "bevel"
	case "fill-rule", "clip-rule":
		return value == "nonzero" || value == "evenodd"
	case "d":
		return svgPathData.MatchString(value)
	case "transform":
		return svgTransform.MatchString(value)
	default:
		return svgNumberList.MatchString(value)
	}
}
