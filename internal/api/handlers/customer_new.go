package handlers

import (
	"math/rand"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/s4lfanet/salfanet-radius-go/internal/db/models"
)

// CustomerNewHandler handles customer portal endpoints not yet in customer_portal.go or customer_ext.go.
type CustomerNewHandler struct{ db *gorm.DB }

func NewCustomerNewHandler(db *gorm.DB) *CustomerNewHandler {
	return &CustomerNewHandler{db: db}
}

func (h *CustomerNewHandler) custID(c fiber.Ctx) string {
	id, _ := c.Locals("customerID").(string)
	return id
}

// PATCH /api/customer/profile
func (h *CustomerNewHandler) UpdateProfile(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "message": "Unauthorized"})
	}
	var body struct {
		Name  *string `json:"name"`
		Phone *string `json:"phone"`
		Email *string `json:"email"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"success": false, "message": "invalid body"})
	}

	updates := map[string]interface{}{}
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if len(name) < 2 {
			return c.Status(400).JSON(fiber.Map{"success": false, "message": "Nama minimal 2 karakter"})
		}
		updates["name"] = name
	}
	if body.Phone != nil {
		updates["phone"] = strings.TrimSpace(*body.Phone)
	}
	if body.Email != nil {
		updates["email"] = strings.TrimSpace(*body.Email)
	}
	if len(updates) == 0 {
		return c.Status(400).JSON(fiber.Map{"success": false, "message": "Tidak ada perubahan"})
	}

	if err := h.db.Model(&models.PppoeUser{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"success": false, "message": err.Error()})
	}

	var user models.PppoeUser
	h.db.First(&user, "id = ?", userID)
	return c.JSON(fiber.Map{"success": true, "message": "Profil berhasil diperbarui", "user": user})
}

// GET /api/customer/referral
func (h *CustomerNewHandler) GetReferral(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	var user models.PppoeUser
	if err := h.db.First(&user, "id = ?", userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "User not found"})
	}

	var company models.Company
	h.db.First(&company)

	var totalReferred int64
	h.db.Model(&models.PppoeUser{}).Where("referred_by_id = ?", userID).Count(&totalReferred)

	var creditedRewards struct {
		Total int64
		Sum   *int
	}
	h.db.Model(&models.ReferralReward{}).
		Select("COUNT(*) as total, SUM(amount) as sum").
		Where("referrer_id = ? AND status = ?", userID, "CREDITED").
		Scan(&creditedRewards)

	var pendingRewards struct {
		Total int64
		Sum   *int
	}
	h.db.Model(&models.ReferralReward{}).
		Select("COUNT(*) as total, SUM(amount) as sum").
		Where("referrer_id = ? AND status = ?", userID, "PENDING").
		Scan(&pendingRewards)

	var recentReferrals []models.PppoeUser
	h.db.Select("id, name, created_at").
		Where("referred_by_id = ?", userID).
		Order("created_at DESC").Limit(10).Find(&recentReferrals)

	baseURL := "http://localhost:3000"
	if company.BaseURL != nil {
		baseURL = *company.BaseURL
	}
	var shareURL *string
	if user.ReferralCode != nil {
		u := baseURL + "/daftar?ref=" + *user.ReferralCode
		shareURL = &u
	}

	creditedSum := 0
	if creditedRewards.Sum != nil {
		creditedSum = *creditedRewards.Sum
	}
	pendingSum := 0
	if pendingRewards.Sum != nil {
		pendingSum = *pendingRewards.Sum
	}

	rewardAmount := 10000
	if company.ReferralRewardAmount != nil {
		rewardAmount = *company.ReferralRewardAmount
	}

	return c.JSON(fiber.Map{
		"success": true,
		"referral": fiber.Map{
			"code":     user.ReferralCode,
			"shareUrl": shareURL,
			"stats": fiber.Map{
				"totalReferred":         totalReferred,
				"totalRewardsCredited":  creditedSum,
				"totalRewardsCount":     creditedRewards.Total,
				"pendingRewardsAmount":  pendingSum,
				"pendingRewardsCount":   pendingRewards.Total,
			},
			"recentReferrals": recentReferrals,
		},
		"config": fiber.Map{
			"enabled":        company.ReferralEnabled,
			"rewardAmount":   rewardAmount,
			"rewardType":     company.ReferralRewardType,
			"rewardBoth":     company.ReferralRewardBoth,
			"referredAmount": company.ReferralReferredAmount,
			"adminPhone":     company.AdminPhone,
		},
	})
}

// POST /api/customer/referral — generate referral code
func (h *CustomerNewHandler) GenerateReferralCode(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	var user models.PppoeUser
	if err := h.db.First(&user, "id = ?", userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "User not found"})
	}

	if user.ReferralCode != nil {
		return c.JSON(fiber.Map{"success": true, "code": user.ReferralCode, "message": "Referral code already exists"})
	}

	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var code string
	for attempts := 0; attempts < 20; attempts++ {
		b := make([]byte, 8)
		for i := range b {
			b[i] = chars[rand.Intn(len(chars))]
		}
		code = string(b)
		var count int64
		h.db.Model(&models.PppoeUser{}).Where("referral_code = ?", code).Count(&count)
		if count == 0 {
			break
		}
	}

	if err := h.db.Model(&user).Update("referral_code", code).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"success": false, "error": err.Error()})
	}

	return c.JSON(fiber.Map{"success": true, "code": code})
}

// GET /api/customer/referral/rewards
func (h *CustomerNewHandler) GetReferralRewards(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	page, limit := pageParams(c)
	var total int64
	h.db.Model(&models.ReferralReward{}).Where("referrer_id = ?", userID).Count(&total)

	var rewards []models.ReferralReward
	h.db.Preload("Referred").
		Where("referrer_id = ?", userID).
		Order("created_at DESC").
		Offset((page - 1) * limit).Limit(limit).
		Find(&rewards)

	return c.JSON(fiber.Map{
		"success": true,
		"rewards": rewards,
		"pagination": fiber.Map{
			"page": page, "limit": limit, "total": total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// POST /api/customer/upgrade-package
func (h *CustomerNewHandler) UpgradePackage(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	var body struct {
		PackageID string `json:"packageId"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.PackageID == "" {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "packageId required"})
	}

	var user models.PppoeUser
	if err := h.db.Preload("Profile").First(&user, "id = ?", userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "User not found"})
	}

	var pkg models.PppoeProfile
	if err := h.db.First(&pkg, "id = ?", body.PackageID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "Package not found"})
	}

	if user.ProfileID == pkg.ID {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "Anda sudah menggunakan paket ini"})
	}

	// Check for existing pending upgrade invoice
	var existing models.Invoice
	if h.db.Where("user_id = ? AND invoice_type = ? AND status = ?", userID, models.InvoiceAddon, models.InvoicePending).
		First(&existing).Error == nil {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "Anda sudah memiliki tagihan upgrade yang belum dibayar"})
	}

	now := time.Now()
	invoiceNumber := "INV-UPG-" + now.Format("20060102") + "-" + strings.ToUpper(uuid.New().String()[:6])
	dueDate := now.Add(24 * time.Hour)

	invoice := models.Invoice{
		ID:               uuid.New().String(),
		InvoiceNumber:    invoiceNumber,
		UserID:           &userID,
		Amount:           pkg.Price,
		Status:           models.InvoicePending,
		DueDate:          dueDate,
		InvoiceType:      models.InvoiceAddon,
		CustomerName:     &user.Name,
		CustomerPhone:    &user.Phone,
		CustomerEmail:    user.Email,
		CustomerUsername: &user.Username,
	}
	if err := h.db.Create(&invoice).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"success": false, "error": err.Error()})
	}

	return c.Status(201).JSON(fiber.Map{
		"success": true,
		"message": "Invoice upgrade berhasil dibuat",
		"invoice": invoice,
		"package": pkg,
	})
}

// GET /api/customer/upgrade
func (h *CustomerNewHandler) GetUpgradeOptions(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	var user models.PppoeUser
	if err := h.db.Preload("Profile").First(&user, "id = ?", userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "User not found"})
	}

	var packages []models.PppoeProfile
	h.db.Where("is_active = ? AND id != ?", true, user.ProfileID).Order("price ASC").Find(&packages)

	return c.JSON(fiber.Map{
		"success":        true,
		"currentPackage": user.Profile,
		"packages":       packages,
	})
}

// POST /api/customer/topup-direct
func (h *CustomerNewHandler) TopupDirect(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	var body struct {
		Amount int `json:"amount"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.Amount <= 0 {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "amount required"})
	}

	var user models.PppoeUser
	if err := h.db.First(&user, "id = ?", userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "User not found"})
	}

	now := time.Now()
	invoiceNumber := "INV-TOP-" + now.Format("20060102") + "-" + strings.ToUpper(uuid.New().String()[:6])
	dueDate := now.Add(24 * time.Hour)

	invoice := models.Invoice{
		ID:               uuid.New().String(),
		InvoiceNumber:    invoiceNumber,
		UserID:           &userID,
		Amount:           body.Amount,
		Status:           models.InvoicePending,
		DueDate:          dueDate,
		InvoiceType:      models.InvoiceTopup,
		CustomerName:     &user.Name,
		CustomerPhone:    &user.Phone,
		CustomerEmail:    user.Email,
		CustomerUsername: &user.Username,
	}
	if err := h.db.Create(&invoice).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"success": false, "error": err.Error()})
	}

	return c.Status(201).JSON(fiber.Map{"success": true, "invoice": invoice})
}

// GET /api/customer/notifications — activity-based notification feed
func (h *CustomerNewHandler) GetNotifications(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	sinceStr := c.Query("since")
	since := time.Now().Add(-30 * 24 * time.Hour)
	if sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			since = t
		}
	}

	type Event struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Title     string `json:"title"`
		Message   string `json:"message"`
		Timestamp string `json:"timestamp"`
	}
	var events []Event

	// Recent invoices
	var invoices []models.Invoice
	h.db.Where("user_id = ? AND created_at >= ?", userID, since).
		Order("created_at DESC").Limit(10).Find(&invoices)
	for _, inv := range invoices {
		events = append(events, Event{
			ID:        "invoice-" + inv.ID,
			Type:      "invoice",
			Title:     "Tagihan Baru",
			Message:   "Tagihan " + inv.InvoiceNumber + " sebesar Rp " + formatAmount(inv.Amount) + " telah dibuat.",
			Timestamp: inv.CreatedAt.Format(time.RFC3339),
		})
	}

	// Paid invoices
	var paid []models.Invoice
	h.db.Where("user_id = ? AND status = ? AND paid_at >= ?", userID, models.InvoicePaid, since).
		Order("paid_at DESC").Limit(10).Find(&paid)
	for _, inv := range paid {
		ts := inv.CreatedAt
		if inv.PaidAt != nil {
			ts = *inv.PaidAt
		}
		events = append(events, Event{
			ID:        "paid-" + inv.ID,
			Type:      "payment_success",
			Title:     "Pembayaran Berhasil",
			Message:   "Pembayaran tagihan " + inv.InvoiceNumber + " telah dikonfirmasi.",
			Timestamp: ts.Format(time.RFC3339),
		})
	}

	// Tickets
	var tickets []models.Ticket
	h.db.Where("customer_id = ? AND created_at >= ?", userID, since).
		Order("created_at DESC").Limit(5).Find(&tickets)
	for _, tk := range tickets {
		events = append(events, Event{
			ID:        "ticket-" + tk.ID,
			Type:      "ticket_created",
			Title:     "Tiket Dibuat",
			Message:   "Tiket #" + tk.TicketNumber + " \"" + tk.Subject + "\" telah dibuat.",
			Timestamp: tk.CreatedAt.Format(time.RFC3339),
		})
	}

	return c.JSON(fiber.Map{"success": true, "events": events})
}

// GET /api/customer/payment-methods
func (h *CustomerNewHandler) GetPaymentMethods(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	var company models.Company
	h.db.First(&company)

	bankAccounts := []interface{}{}
	if company.BankAccounts != nil && *company.BankAccounts != "" {
		bankAccounts = []interface{}{*company.BankAccounts}
	}

	return c.JSON(fiber.Map{
		"success": true,
		"methods": fiber.Map{
			"bankTransfer": bankAccounts,
			"online":       []string{},
		},
	})
}

// GET /api/customer/invoices/:id
func (h *CustomerNewHandler) GetInvoiceDetail(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	var invoice models.Invoice
	if err := h.db.First(&invoice, "id = ? AND user_id = ?", c.Params("id"), userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "Invoice not found"})
	}

	return c.JSON(fiber.Map{"success": true, "invoice": invoice})
}

// POST /api/customer/invoices/:id/manual-payment
func (h *CustomerNewHandler) InvoiceManualPayment(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}

	var invoice models.Invoice
	if err := h.db.First(&invoice, "id = ? AND user_id = ?", c.Params("id"), userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "Invoice not found"})
	}
	if invoice.Status != models.InvoicePending {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "Invoice sudah dibayar atau dibatalkan"})
	}

	var body struct {
		BankName     string  `json:"bankName"`
		AccountName  string  `json:"accountName"`
		Amount       float64 `json:"amount"`
		TransferDate string  `json:"transferDate"`
		ProofImage   *string `json:"proofImage"`
		Notes        *string `json:"notes"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "invalid body"})
	}

	transferDate := time.Now()
	if body.TransferDate != "" {
		if t, err := time.Parse("2006-01-02", body.TransferDate); err == nil {
			transferDate = t
		}
	}

	mp := models.ManualPayment{
		ID:           uuid.New().String(),
		InvoiceID:    invoice.ID,
		PppoeUserID:  userID,
		Amount:       body.Amount,
		BankName:     body.BankName,
		AccountName:  body.AccountName,
		TransferDate: transferDate,
		ProofImage:   body.ProofImage,
		Notes:        body.Notes,
		Status:       "PENDING",
	}
	if err := h.db.Create(&mp).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"success": false, "error": err.Error()})
	}

	return c.Status(201).JSON(fiber.Map{"success": true, "payment": mp})
}

func formatAmount(amount int) string {
	s := ""
	n := amount
	for n > 0 {
		if s != "" && len(s)%4 == 3 {
			s = "." + s
		}
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if s == "" {
		return "0"
	}
	return s
}

// POST /api/customer/notifications/:id/read — mark notification read (stub, notifications are event-sourced)
func (h *CustomerNewHandler) MarkNotificationRead(c fiber.Ctx) error {
	if h.custID(c) == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/customer/payments
func (h *CustomerNewHandler) GetPayments(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}
	page, limit := pageParams(c)
	var total int64
	h.db.Model(&models.ManualPayment{}).Where("pppoe_user_id = ?", userID).Count(&total)
	var payments []models.ManualPayment
	h.db.Preload("Invoice").
		Where("pppoe_user_id = ?", userID).
		Order("created_at DESC").
		Offset((page-1)*limit).Limit(limit).Find(&payments)
	return c.JSON(fiber.Map{
		"success":  true,
		"payments": payments,
		"pagination": fiber.Map{
			"page": page, "limit": limit, "total": total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// POST /api/customer/payments/:id/proof — upload payment proof URL
func (h *CustomerNewHandler) UploadPaymentProof(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}
	id := c.Params("id")
	var mp models.ManualPayment
	if err := h.db.First(&mp, "id = ? AND pppoe_user_id = ?", id, userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "Payment not found"})
	}
	var body struct {
		ProofImage string `json:"proofImage"`
	}
	if err := c.Bind().JSON(&body); err != nil || body.ProofImage == "" {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "proofImage required"})
	}
	h.db.Model(&mp).Update("proof_image", body.ProofImage)
	return c.JSON(fiber.Map{"success": true, "payment": mp})
}

// POST /api/customer/ont/reboot — stub (requires GenieACS)
func (h *CustomerNewHandler) ONTReboot(c fiber.Ctx) error {
	if h.custID(c) == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}
	return c.JSON(fiber.Map{"success": false, "error": "ONT reboot requires GenieACS integration"})
}

// GET /api/customer/wifi — stub (requires GenieACS)
func (h *CustomerNewHandler) GetWifi(c fiber.Ctx) error {
	if h.custID(c) == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}
	return c.JSON(fiber.Map{"success": false, "reason": "not_configured", "error": "GenieACS not configured"})
}

// POST /api/customer/wifi — stub (requires GenieACS)
func (h *CustomerNewHandler) UpdateWifi(c fiber.Ctx) error {
	if h.custID(c) == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}
	return c.JSON(fiber.Map{"success": false, "error": "WiFi update requires GenieACS integration"})
}

// POST /api/customer/invoices/:id/regenerate-payment
func (h *CustomerNewHandler) RegeneratePayment(c fiber.Ctx) error {
	userID := h.custID(c)
	if userID == "" {
		return c.Status(401).JSON(fiber.Map{"success": false, "error": "Unauthorized"})
	}
	id := c.Params("id")
	var inv models.Invoice
	if err := h.db.First(&inv, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"success": false, "error": "Invoice not found"})
	}
	if inv.Status == models.InvoicePaid {
		return c.Status(400).JSON(fiber.Map{"success": false, "error": "Invoice already paid"})
	}
	return c.JSON(fiber.Map{"success": false, "error": "Payment gateway not configured"})
}
