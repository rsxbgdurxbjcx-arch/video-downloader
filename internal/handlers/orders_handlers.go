package handlers

import (
	"errors"
	"log"
	"net/http"

	"video-downloader/internal/models"
	"video-downloader/internal/services"
)

// ---- POST /api/orders（登录 + 已验证 -> 创建订单，只接收 plan_id）----
func (a *App) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if !user.IsVerified() || user.Status != models.StatusActive {
		writeErr(w, http.StatusForbidden, "账户尚未完成邮箱验证，无法创建订单")
		return
	}
	var req struct {
		PlanID int64 `json:"plan_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if req.PlanID <= 0 {
		writeErr(w, http.StatusBadRequest, "缺少 plan_id")
		return
	}
	order, err := a.Order.CreateOrder(r.Context(), user.ID, req.PlanID)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrPlanNotFound):
			writeErr(w, http.StatusNotFound, "套餐不存在")
		case errors.Is(err, services.ErrPlanDisabled):
			writeErr(w, http.StatusBadRequest, "套餐已停用")
		default:
			log.Printf("[order] 创建订单失败: %v", err)
			writeErr(w, http.StatusInternalServerError, "创建订单失败")
		}
		return
	}
	writeJSON(w, http.StatusOK, orderJSON(order))
}

// ---- GET /api/orders（本人订单）----
func (a *App) handleListOrders(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	page, size := pageParams(r)
	orders, err := a.Store.ListUserOrders(r.Context(), user.ID, size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(orders))
	for _, o := range orders {
		out = append(out, orderJSON(o))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"orders": out, "page": page})
}

// ---- GET /api/orders/{orderNo}（本人或管理员）----
func (a *App) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	orderNo := r.PathValue("no")
	order, err := a.Store.GetOrderByNo(r.Context(), orderNo)
	if err != nil {
		writeErr(w, http.StatusNotFound, "订单不存在")
		return
	}
	if order.UserID != user.ID && !user.IsAdmin() {
		writeErr(w, http.StatusForbidden, "无权查看该订单")
		return
	}
	writeJSON(w, http.StatusOK, orderJSON(order))
}

// ---- POST /api/orders/{orderNo}/close（本人 pending 订单）----
func (a *App) handleCloseOrder(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	orderNo := r.PathValue("no")
	order, err := a.Store.GetOrderByNo(r.Context(), orderNo)
	if err != nil {
		writeErr(w, http.StatusNotFound, "订单不存在")
		return
	}
	if order.UserID != user.ID && !user.IsAdmin() {
		writeErr(w, http.StatusForbidden, "无权操作该订单")
		return
	}
	updated, err := a.Order.CloseOrder(r.Context(), orderNo)
	if err != nil {
		if errors.Is(err, services.ErrOrderPaid) {
			writeErr(w, http.StatusBadRequest, "已支付订单不能关闭")
			return
		}
		writeErr(w, http.StatusBadRequest, "无法关闭订单")
		return
	}
	writeJSON(w, http.StatusOK, orderJSON(updated))
}

// ---- POST /api/orders/{orderNo}/simulate-pay（仅开发环境）----
func (a *App) handleSimulatePay(w http.ResponseWriter, r *http.Request) {
	// 生产环境绝对禁止模拟支付
	if a.Cfg.IsProduction() || !a.PayMgr.MockEnabled() {
		writeErr(w, http.StatusNotFound, "接口不存在")
		return
	}
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if !user.IsVerified() {
		writeErr(w, http.StatusForbidden, "账户尚未完成邮箱验证，无法使用模拟支付")
		return
	}
	orderNo := r.PathValue("no")
	order, err := a.Store.GetOrderByNo(r.Context(), orderNo)
	if err != nil {
		writeErr(w, http.StatusNotFound, "订单不存在")
		return
	}
	if order.UserID != user.ID && !user.IsAdmin() {
		writeErr(w, http.StatusForbidden, "无权操作该订单")
		return
	}
	provider, perr := a.PayMgr.Get("mock")
	if perr != nil {
		writeErr(w, http.StatusServiceUnavailable, "支付渠道不可用")
		return
	}
	if err := a.Order.ProcessPaidEvent(r.Context(), provider, orderNo, `{"simulated":true}`); err != nil {
		log.Printf("[pay] 模拟支付处理失败: order=%s err=%v", orderNo, err)
		writeErr(w, http.StatusInternalServerError, "支付处理失败")
		return
	}
	updated, _ := a.Store.GetOrderByNo(r.Context(), orderNo)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"order":  orderJSON(updated),
		"notice": "模拟支付成功（仅开发环境）",
	})
}

// ---- POST /api/orders/{orderNo}/refund（管理员；开发阶段功能）----
func (a *App) handleRefundOrder(w http.ResponseWriter, r *http.Request) {
	orderNo := r.PathValue("no")
	order, err := a.Order.RefundOrder(r.Context(), orderNo)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "退款失败："+err.Error())
		return
	}
	a.RecordAudit(r, "order.refund", "管理员标记退款: "+orderNo)
	writeJSON(w, http.StatusOK, orderJSON(order))
}

// orderJSON 订单 JSON（不含敏感数据）。
func orderJSON(o *models.Order) map[string]interface{} {
	return map[string]interface{}{
		"order_no":          o.OrderNo,
		"plan_id":           o.PlanID,
		"amount_cents":      o.AmountCents,
		"amount":            formatCents(o.AmountCents),
		"currency":          o.Currency,
		"provider":          o.Provider,
		"provider_trade_no": o.ProviderTradeNo,
		"status":            o.Status,
		"subject":           o.Subject,
		"paid_at":           nilTimeUnix(o.PaidAt),
		"created_at":        o.CreatedAt.Unix(),
		"expires_at":        o.ExpiresAt.Unix(),
		"updated_at":        o.UpdatedAt.Unix(),
	}
}
