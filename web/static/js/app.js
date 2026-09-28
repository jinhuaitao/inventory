/* ==========================================================================
   库存管理系统 · 前端交互
   纯原生实现，无任何外部依赖。所有脚本均由 /static/js/app.js 提供，
   页面数据通过 data-* 属性传递，以满足严格的 CSP 策略。
   ========================================================================== */

(function () {
  'use strict';

  var $  = function (sel, root) { return (root || document).querySelector(sel); };
  var $$ = function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); };

  /* ---------------------------------------------------------------- 侧边栏 */
  function initSidebar() {
    var toggle = $('#sidebarToggle');
    if (!toggle) return;

    toggle.addEventListener('click', function () {
      document.body.classList.toggle('sidebar-open');
    });

    // 点击遮罩关闭
    document.addEventListener('click', function (e) {
      if (!document.body.classList.contains('sidebar-open')) return;
      if (e.target.closest('.sidebar') || e.target.closest('#sidebarToggle')) return;
      document.body.classList.remove('sidebar-open');
    });

    // 侧边栏内导航后自动收起
    $$('.sidebar .nav-item').forEach(function (item) {
      item.addEventListener('click', function () {
        document.body.classList.remove('sidebar-open');
      });
    });
  }

  /* ------------------------------------------------------------ 用户菜单 */
  function initUserMenu() {
    var menus = $$('.user-menu');
    if (!menus.length) return;

    document.addEventListener('click', function (e) {
      menus.forEach(function (menu) {
        if (!menu.contains(e.target)) menu.removeAttribute('open');
      });
    });

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') menus.forEach(function (m) { m.removeAttribute('open'); });
    });
  }

  /* -------------------------------------------------------- 密码显示切换 */
  function initPasswordToggles() {
    $$('[data-toggle-password]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var input = document.getElementById(btn.getAttribute('data-toggle-password'));
        if (!input) return;
        var show = input.type === 'password';
        input.type = show ? 'text' : 'password';
        btn.textContent = show ? '隐藏' : '显示';
      });
    });
  }

  /* ------------------------------------------------------------ 密码强度 */
  // 与后端 utils.PasswordStrength 保持一致的评分规则
  function scorePassword(pw) {
    if (!pw) return 0;
    var score = 0;
    if (pw.length >= 8) score++;
    if (pw.length >= 12) score++;
    if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) score++;
    if (/[0-9]/.test(pw)) score++;
    if (/[^A-Za-z0-9]/.test(pw)) score++;
    return Math.min(score, 4);
  }

  var STRENGTH_TEXT = ['', '弱', '一般', '较强', '很强'];

  function initStrengthMeters() {
    $$('[data-strength-input]').forEach(function (input) {
      var meter = $('[data-strength-meter]', input.closest('.field') || document);
      if (!meter) return;
      var label = $('.strength-label', meter);

      var update = function () {
        var level = scorePassword(input.value);
        meter.setAttribute('data-level', String(level));
        if (label) label.textContent = input.value ? (STRENGTH_TEXT[level] || '') : '';
      };

      input.addEventListener('input', update);
      update();
    });
  }

  /* -------------------------------------------------------- 危险操作确认 */
  function initConfirm() {
    $$('form[data-confirm]').forEach(function (form) {
      form.addEventListener('submit', function (e) {
        var msg = form.getAttribute('data-confirm');
        if (msg && !window.confirm(msg)) {
          e.preventDefault();
        }
      });
    });
  }

  /* ------------------------------------------------------ 出入库表单联动 */
  function formatMoney(value) {
    var neg = value < 0;
    value = Math.abs(value);
    var parts = value.toFixed(2).split('.');
    parts[0] = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, ',');
    return (neg ? '-¥' : '¥') + parts[0] + '.' + parts[1];
  }

  function initStockForm() {
    var form = $('#stockForm');
    if (!form) return;

    var select    = $('[data-product-select]', form);
    var qtyInput  = form.querySelector('input[name="quantity"], input[name="actual_quantity"]');
    var costInput = $('[data-cost-input]', form);
    var saleInput = $('[data-sale-input]', form);
    var hint      = $('[data-product-hint]', form);
    var qtyHint   = $('[data-qty-hint]', form);
    var preview   = $('[data-calc-preview]', form);
    var previewEl = $('[data-calc-value]', form);
    var diffEl    = $('[data-diff-preview]', form);
    var isAdjust  = !!diffEl;

    if (!select) return;

    function selectedOption() {
      return select.options[select.selectedIndex] || null;
    }

    function currentStock() {
      var opt = selectedOption();
      if (!opt || !opt.value) return null;
      var n = parseInt(opt.getAttribute('data-qty') || '0', 10);
      return isNaN(n) ? 0 : n;
    }

    function refreshProductInfo() {
      var opt = selectedOption();
      if (!opt || !opt.value) {
        if (hint) hint.textContent = '';
        if (qtyHint) qtyHint.textContent = '';
        if (preview) preview.hidden = true;
        if (diffEl) { diffEl.textContent = '—'; diffEl.className = 'diff-preview'; }
        return;
      }

      var stock = currentStock();
      var unit  = opt.getAttribute('data-unit') || '件';
      var cost  = parseFloat(opt.getAttribute('data-cost') || '0');
      var sale  = parseFloat(opt.getAttribute('data-sale') || '0');
      var safety = parseInt(opt.getAttribute('data-safety') || '0', 10);

      if (hint) {
        var text = '当前库存 ' + stock + ' ' + unit;
        if (safety > 0) text += ' · 安全库存 ' + safety;
        if (stock <= 0) text += ' · 已缺货';
        else if (stock <= safety) text += ' · 库存偏低';
        hint.textContent = text;
        hint.style.color = stock <= safety ? 'var(--warning)' : 'var(--text-muted)';
      }

      // 自动填充单价（仅在用户尚未填写时）
      if (costInput && !costInput.dataset.touched && !costInput.value && cost > 0) {
        costInput.value = cost.toFixed(2);
      }
      if (saleInput && !saleInput.dataset.touched && !saleInput.value && sale > 0) {
        saleInput.value = sale.toFixed(2);
      }

      updateQuantityState();
    }

    function updateQuantityState() {
      var stock = currentStock();
      var unit  = '';
      var opt   = selectedOption();
      if (opt && opt.value) unit = opt.getAttribute('data-unit') || '件';

      var qty = parseInt(qtyInput && qtyInput.value ? qtyInput.value : '0', 10);
      if (isNaN(qty)) qty = 0;

      // 出库校验
      if (qtyInput && !isAdjust && stock !== null) {
        var isOut = !!saleInput; // 出库表单带 data-sale-input
        if (isOut && qty > stock) {
          qtyInput.classList.add('is-invalid');
          if (qtyHint) {
            qtyHint.textContent = '超出当前库存 ' + stock + ' ' + unit;
            qtyHint.style.color = 'var(--danger)';
          }
        } else {
          qtyInput.classList.remove('is-invalid');
          if (qtyHint) {
            qtyHint.textContent = stock !== null && qty > 0
              ? '出库后剩余 ' + (stock - qty) + ' ' + unit
              : '';
            qtyHint.style.color = 'var(--text-muted)';
          }
        }
      }

      // 金额预览
      if (preview && previewEl && !isAdjust) {
        var price = 0;
        if (costInput && costInput.value) price = parseFloat(costInput.value) || 0;
        else if (saleInput && saleInput.value) price = parseFloat(saleInput.value) || 0;

        if (qty > 0 && price > 0) {
          previewEl.textContent = formatMoney(qty * price);
          preview.hidden = false;
        } else {
          preview.hidden = true;
        }
      }

      // 盘点差异
      if (diffEl && stock !== null) {
        var diff = qty - stock;
        if (qtyInput.value === '') {
          diffEl.textContent = '—';
          diffEl.className = 'diff-preview';
        } else if (diff === 0) {
          diffEl.textContent = '无差异';
          diffEl.className = 'diff-preview zero';
        } else if (diff > 0) {
          diffEl.textContent = '盘盈 +' + diff + ' ' + unit;
          diffEl.className = 'diff-preview positive';
        } else {
          diffEl.textContent = '盘亏 ' + diff + ' ' + unit;
          diffEl.className = 'diff-preview negative';
        }
      }
    }

    select.addEventListener('change', refreshProductInfo);
    if (qtyInput) qtyInput.addEventListener('input', updateQuantityState);

    [costInput, saleInput].forEach(function (el) {
      if (!el) return;
      el.addEventListener('input', function () {
        el.dataset.touched = '1';
        updateQuantityState();
      });
    });

    refreshProductInfo();
  }

  /* ------------------------------------------------------------ 自动聚焦 */
  function initAutofocus() {
    // 页面若已标记 autofocus 的输入框，光标移到末尾
    var el = document.querySelector('[autofocus]');
    if (el && el.setSelectionRange && el.value) {
      try { el.setSelectionRange(el.value.length, el.value.length); } catch (_) { /* 忽略 */ }
    }
  }

  /* ---------------------------------------------------------------- 启动 */
  function init() {
    initSidebar();
    initUserMenu();
    initPasswordToggles();
    initStrengthMeters();
    initConfirm();
    initStockForm();
    initAutofocus();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
