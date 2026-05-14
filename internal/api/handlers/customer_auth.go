package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/s4lfanet/salfanet-radius-go/internal/db/models"
	"github.com/s4lfanet/salfanet-radius-go/internal/notify"
)

type CustomerAuthHandler struct{ db *gorm.DB }

func NewCustomerAuthHandler(db *gorm.DB) *CustomerAuthHandler {
	return &CustomerAuthHandler{db: db}
}

func normalizeCustomerPhone(phone string) string {
	clean := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if strings.HasPrefix(clean, "0") {
		clean = "62" + clean[1:]
	}
	if !strings.HasPrefix(clean, "62") && len(clean) > 8 {
		clean = "62" + clean
	}
	return clean
}

func generateCustomerToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func mustRandInt(max int) int {
	b := make([]byte, 4)
	rand.Read(b)
	n := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	if n < 0 {
		n = -n
	}
	return n % max
}

func strPtr(s string) *string { return &s }

func (h *CustomerAuthHandler) findUserByPhoneOrID(input string) (*models.PppoeUser, error) {
	clean := normalizeCustomerPhone(input)
	local := "0" + clean[2:]
	var user models.PppoeUser
	err := h.db.Preload("Profile").First(&user,
		"phone = ? OR phone = ? OR phone = ? OR customerId = ?",
		input, clean, local, input).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (h *CustomerAuthHandler) otpSettings() (otpEnabled bool, otpExpiry int) {
	var s models.WhatsappReminderSetting
	if h.db.First(&s).Error == nil {
		return s.OtpEnabled, s.OtpExpiry
	}
	return true, 5
}

// POST /api/customer/auth/send-otp
func (h *CustomerAuthHandler) SendOTP(c fiber.Ctx) error {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.Phone == "" {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "phone required"})
	}

	otpEnabled, otpExpiry := h.otpSettings()
	if !otpEnabled {
		return c.Status(403).JSON(fiber.Map{"success": false, "error": "OTP login is currently disabled"})
	}

	clean := normalizeCustomerPhone(body.Phone)
	local := "0" + clean[2:]

	var user models.PppoeUser
	if err := h.db.First(&user, "phone = ? OR phone = ? OR phone = ?", body.Phone, clean, local).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "Phone number not registered"})
	}

	var count int64
	h.db.Model(&models.CustomerSession{}).
		Where("phone = ? AND created_at >= ?", clean, time.Now().Add(-15*time.Minute)).
		Count(&count)
	if count >= 3 {
		return c.Status(429).JSON(fiber.Map{"success": false, "error": "Too many OTP requests. Please try again in 15 minutes."})
	}

	otp := fmt.Sprintf("%06d", mustRandInt(1000000))
	expiry := time.Now().Add(time.Duration(otpExpiry) * time.Minute)

	h.db.Create(&models.CustomerSession{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Phone:     clean,
		OTPCode:   &otp,
		OTPExpiry: &expiry,
		Verified:  false,
	})

	var company models.Company
	h.db.First(&company)
	companyName := company.Name
	if companyName == "" {
		companyName = "SALFANET RADIUS"
	}

	msg := fmt.Sprintf("Kode OTP Anda: %s\n\nBerlaku %d menit.\nJangan bagikan kode ini kepada siapapun.\n\n- %s", otp, otpExpiry, companyName)
	if err := notify.Send(clean, msg); err != nil {
		h.db.Where("phone = ? AND otp_code = ?", clean, otp).Delete(&models.CustomerSession{})
		return c.Status(500).JSON(fiber.Map{"success": false, "error": "Failed to send OTP. Please try again."})
	}

	return c.JSON(fiber.Map{"success": true, "message": "OTP sent successfully", "expiresIn": otpExpiry})
}

// POST /api/customer/auth/verify-otp
func (h *CustomerAuthHandler) VerifyOTP(c fiber.Ctx) error {
	var body struct {
		Phone   string `json:"phone"`
		OTPCode string `json:"otpCode"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.Phone == "" || body.OTPCode == "" {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "phone and otpCode required"})
	}

	clean := normalizeCustomerPhone(body.Phone)

	var session models.CustomerSession
	if err := h.db.Where("phone = ? AND otp_code = ? AND verified = ?", clean, body.OTPCode, false).
		Order("created_at DESC").First(&session).Error; err != nil {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "Invalid OTP code"})
	}

	if session.OTPExpiry != nil && time.Now().After(*session.OTPExpiry) {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "OTP code has expired"})
	}

	token := generateCustomerToken()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	h.db.Model(&session).Updates(map[string]interface{}{
		"verified":  true,
		"token":     token,
		"expiresAt": expiresAt,
		"otpCode":   nil,
		"otpExpiry": nil,
	})

	var user models.PppoeUser
	h.db.Preload("Profile").First(&user, "id = ?", session.UserID)

	return c.JSON(fiber.Map{
		"success":   true,
		"message":   "Login successful",
		"token":     token,
		"expiresAt": expiresAt,
		"user":      user,
	})
}

// POST /api/customer/auth/login
func (h *CustomerAuthHandler) Login(c fiber.Ctx) error {
	var body struct {
		Phone      string `json:"phone"`
		Identifier string `json:"identifier"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "invalid body"})
	}
	input := body.Identifier
	if input == "" {
		input = body.Phone
	}
	if input == "" {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "Phone number or customer ID is required"})
	}

	user, err := h.findUserByPhoneOrID(input)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "Phone number or customer ID not registered"})
	}

	otpEnabled, _ := h.otpSettings()
	userPhone := normalizeCustomerPhone(user.Phone)

	if !otpEnabled {
		token := generateCustomerToken()
		expiresAt := time.Now().Add(7 * 24 * time.Hour)
		h.db.Create(&models.CustomerSession{
			ID:        uuid.New().String(),
			UserID:    user.ID,
			Phone:     userPhone,
			Token:     &token,
			ExpiresAt: &expiresAt,
			Verified:  true,
		})
		return c.JSON(fiber.Map{
			"success":    true,
			"otpEnabled": false,
			"requireOTP": false,
			"token":      token,
			"user":       user,
		})
	}

	return c.JSON(fiber.Map{
		"success":    true,
		"otpEnabled": true,
		"requireOTP": true,
		"phone":      userPhone,
		"user": fiber.Map{
			"id":       user.ID,
			"name":     user.Name,
			"phone":    user.Phone,
			"username": user.Username,
		},
	})
}

// POST /api/customer/auth/bypass-login
func (h *CustomerAuthHandler) BypassLogin(c fiber.Ctx) error {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.Phone == "" {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "phone required"})
	}

	otpEnabled, _ := h.otpSettings()
	if otpEnabled {
		return c.Status(403).JSON(fiber.Map{"success": false, "error": "Bypass login not available when OTP is enabled"})
	}

	clean := normalizeCustomerPhone(body.Phone)
	local := "0" + clean[2:]

	var user models.PppoeUser
	if err := h.db.Preload("Profile").First(&user, "phone = ? OR phone = ? OR phone = ?", body.Phone, clean, local).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "Phone number not registered"})
	}

	token := generateCustomerToken()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	h.db.Create(&models.CustomerSession{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Phone:     clean,
		Token:     &token,
		ExpiresAt: &expiresAt,
		Verified:  true,
	})

	h.db.Create(&models.ActivityLog{
		ID:          uuid.New().String(),
		UserID:      &user.ID,
		Username:    user.Username,
		Module:      "customer_auth",
		Action:      "bypass_login",
		Description: fmt.Sprintf("Customer %s logged in without OTP (OTP disabled)", user.Username),
		IPAddress:   strPtr(c.IP()),
	})

	return c.JSON(fiber.Map{"success": true, "token": token, "user": user})
}
