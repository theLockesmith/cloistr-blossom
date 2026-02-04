package core

import (
	"context"

	"git.coldforge.xyz/coldforge/coldforge-blossom/internal/cache"
)

type Services interface {
	Init(context.Context) error
	Blob() BlobStorage
	ACR() ACRStorage
	Mime() MimeTypeService
	Settings() SettingService
	Stats() StatService
	Quota() QuotaService
	Cache() cache.Cache
}
