package gin

import (
	"fmt"
	"net/http"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// PaymentMiddleware handles BUD-07 payment requirements for uploads.
// It checks if payment is required and validates payment proofs.
func PaymentMiddleware(paymentService core.PaymentService, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip if payment service is not enabled or not available
		if paymentService == nil || !paymentService.IsEnabled() {
			c.Next()
			return
		}

		// Only check payments for upload operations
		if c.Request.Method != "PUT" && c.Request.Method != "POST" {
			c.Next()
			return
		}

		// Get pubkey from auth middleware (must run after auth)
		pubkey := c.GetString("pk")
		if pubkey == "" {
			// No authenticated user - let other middleware handle this
			c.Next()
			return
		}

		// Get content length
		contentLength := c.Request.ContentLength
		if contentLength <= 0 {
			// Can't determine size - let upload handler deal with it
			c.Next()
			return
		}

		// Check for payment proof in headers
		cashuProof := c.GetHeader("X-Cashu")
		lightningProof := c.GetHeader("X-Lightning")
		paymentRequestID := c.GetHeader("X-Payment-Request")

		// If proof provided, validate it
		if cashuProof != "" || lightningProof != "" {
			var proof *core.PaymentProof
			if cashuProof != "" {
				proof = &core.PaymentProof{
					Method:    core.PaymentMethodCashu,
					Data:      cashuProof,
					RequestID: paymentRequestID,
				}
			} else {
				proof = &core.PaymentProof{
					Method:    core.PaymentMethodLightning,
					Data:      lightningProof,
					RequestID: paymentRequestID,
				}
			}

			// Validate the proof
			if err := paymentService.ValidatePaymentProof(c.Request.Context(), proof); err != nil {
				log.Warn("payment proof validation failed",
					zap.String("pubkey", pubkey),
					zap.String("method", string(proof.Method)),
					zap.Error(err))

				c.Header("X-Reason", err.Error())
				c.AbortWithStatusJSON(http.StatusBadRequest, apiError{
					Message: fmt.Sprintf("invalid payment proof: %s", err.Error()),
				})
				return
			}

			// Payment verified - continue
			log.Info("payment verified",
				zap.String("pubkey", pubkey),
				zap.String("method", string(proof.Method)))
			metrics.PaymentsVerifiedTotal.WithLabelValues(string(proof.Method)).Inc()
			c.Next()
			return
		}

		// No proof provided - check if payment is required
		canFree, err := paymentService.CanUploadFree(c.Request.Context(), pubkey, contentLength)
		if err != nil {
			log.Error("failed to check free upload eligibility", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: "payment check failed",
			})
			return
		}

		if canFree {
			// User can upload for free - consume from free tier after upload succeeds
			// We'll set a flag for the upload handler to consume the free tier
			c.Set("consume_free_tier", true)
			c.Set("free_tier_bytes", contentLength)
			c.Next()
			return
		}

		// Payment required - create payment request
		paymentReq, err := paymentService.CreatePaymentRequest(c.Request.Context(), pubkey, contentLength)
		if err != nil {
			log.Error("failed to create payment request", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusInternalServerError, apiError{
				Message: "failed to create payment request",
			})
			return
		}

		if paymentReq == nil {
			// Somehow no payment required (shouldn't happen given checks above)
			c.Next()
			return
		}

		// Set payment headers and track metrics
		if paymentReq.LightningInvoice != "" {
			c.Header("X-Lightning", paymentReq.LightningInvoice)
			metrics.PaymentRequestsTotal.WithLabelValues("lightning").Inc()
		}
		if paymentReq.CashuRequest != "" {
			c.Header("X-Cashu", paymentReq.CashuRequest)
			metrics.PaymentRequestsTotal.WithLabelValues("cashu").Inc()
		}
		c.Header("X-Payment-Request", paymentReq.ID)
		c.Header("X-Payment-Amount", fmt.Sprintf("%d", paymentReq.AmountSats))

		log.Info("payment required for upload",
			zap.String("pubkey", pubkey),
			zap.Int64("bytes", contentLength),
			zap.Int64("amount_sats", paymentReq.AmountSats),
			zap.String("request_id", paymentReq.ID))

		metrics.PaymentRequiredTotal.Inc()
		c.AbortWithStatusJSON(http.StatusPaymentRequired, apiError{
			Message: fmt.Sprintf("payment required: %d sats", paymentReq.AmountSats),
		})
	}
}

// ConsumeFreeTierMiddleware is a post-upload middleware that consumes the free tier
// after a successful upload. It should be called from the upload handler on success.
func ConsumeFreeTier(c *gin.Context, paymentService core.PaymentService, log *zap.Logger) {
	if paymentService == nil || !paymentService.IsEnabled() {
		return
	}

	shouldConsumeVal, exists := c.Get("consume_free_tier")
	if !exists {
		return
	}
	shouldConsume, ok := shouldConsumeVal.(bool)
	if !ok || !shouldConsume {
		return
	}

	pubkey := c.GetString("pk")
	bytesVal, exists := c.Get("free_tier_bytes")
	if !exists || pubkey == "" {
		return
	}
	bytesInt64, ok := bytesVal.(int64)
	if !ok {
		return
	}
	if err := paymentService.ConsumeFreeTier(c.Request.Context(), pubkey, bytesInt64); err != nil {
		log.Warn("failed to consume free tier",
			zap.String("pubkey", pubkey),
			zap.Int64("bytes", bytesInt64),
			zap.Error(err))
	} else {
		log.Debug("consumed free tier",
			zap.String("pubkey", pubkey),
			zap.Int64("bytes", bytesInt64))
		metrics.FreeTierUploadsTotal.Inc()
		metrics.FreeTierBytesUsed.Add(float64(bytesInt64))
	}
}
