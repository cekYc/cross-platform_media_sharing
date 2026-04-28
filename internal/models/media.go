package models

const (
	MediaTypePhoto     = "photo"
	MediaTypeVideo     = "video"
	MediaTypeAnimation = "animation"
	MediaTypeDocument  = "document"
)

type MediaEvent struct {
	EventID        string
	FileName       string
	FileHash       string
	Caption        string
	SourcePlatform string
	SourceID       string
	TargetPlatform string
	TargetID       string
	MediaGroupID   string
	MediaType      string
	ContentType    string
	AvailableAt    int64 // If set, delay delivery until this Unix timestamp
	SenderName     string
	ReplyToSender  string
	ReplyToCaption string
	FileURL        string // For streaming instead of loading into RAM
}
