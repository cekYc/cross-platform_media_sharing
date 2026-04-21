package models

const (
	MediaTypePhoto     = "photo"
	MediaTypeVideo     = "video"
	MediaTypeAnimation = "animation"
	MediaTypeDocument  = "document"
)

type MediaEvent struct {
	EventID      string
	Data         []byte
	FileName     string
	Caption      string
	SourceTGID   string
	TargetDCID   string
	MediaGroupID string
	MediaType    string
	ContentType  string
}
