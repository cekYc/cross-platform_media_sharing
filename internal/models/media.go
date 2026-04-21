package models

const (
	MediaTypePhoto     = "photo"
	MediaTypeVideo     = "video"
	MediaTypeAnimation = "animation"
	MediaTypeDocument  = "document"
)

type MediaEvent struct {
	Data         []byte
	FileName     string
	Caption      string
	SourceTGID   string
	MediaGroupID string
	MediaType    string
	ContentType  string
}
