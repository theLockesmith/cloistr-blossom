package core

import (
	"context"

	"git.coldforge.xyz/coldforge/cloistr-blossom/internal/cache"
)

type Services interface {
	Init(context.Context) error
	Blob() BlobStorage
	ACR() ACRStorage
	Mime() MimeTypeService
	Settings() SettingService
	Stats() StatService
	Quota() QuotaService
	Moderation() ModerationService
	Cache() cache.Cache
}
