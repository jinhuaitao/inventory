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

  /* ---------------------------------------------------- 提示条关闭 / 返回 */
  // 这两处的原实现分别用了内联 onclick 与 href="javascript:"，
  // 都会被 CSP 的 script-src 'self' 拦掉（内联事件处理器与 javascript:
  // 伪协议同样属于内联脚本），表现为「点了没反应」。改为 data-* + 本文件绑定。
  function initFlashDismiss() {
    $$('[data-dismiss-flash]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var box = btn.closest('.flash') || btn.parentElement;
        if (box) box.remove();
      });
    });
  }

  function initHistoryBack() {
    $$('[data-history-back]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        if (window.history.length > 1) window.history.back();
        else window.location.assign('/');
      });
    });
  }

  /* ---------------------------------------------------- 重启等待与自动刷新 */
  // 「更新完成 / 数据恢复」后展示的等待页。
  //
  // ⚠️ 这段逻辑必须放在本文件里，不能内联在模板的 <script> 中：
  //    站点 CSP 是 script-src 'self'（见 middleware.SecurityHeaders），
  //    内联脚本会被浏览器直接拒绝执行，倒计时永远停在初始值，
  //    页面也就永远不会自动刷新。
  //
  // 行为：倒计时走完后**轮询就绪探针**，探针通过才跳转。
  // 之所以不是「定时到了直接跳」，是因为重启链路耗时并不固定 ——
  // Restart 有延迟 → syscall.Exec 替换进程映像 → 重新打开数据库 →
  // 跑迁移 → 重新监听端口，恢复数据时还要先归档旧库、清 WAL、换入新库。
  // 固定几秒后跳转，很容易在服务尚未就绪时落到浏览器的错误页，且没有重试。
  //
  // 容器上的 data-*：
  //   data-restart-target   就绪后跳转的地址
  //   data-restart-probe    就绪探针，默认 /readyz（同源、无需登录）
  //   data-restart-seconds  跳转前的倒计时秒数，默认 5
  function initRestartWatcher() {
    var box = $('[data-restart-target]');
    if (!box) return;

    var target = box.getAttribute('data-restart-target');
    if (!target) return;

    var probeURL = box.getAttribute('data-restart-probe') || '/readyz';
    var countdownEl = $('[data-restart-countdown]', box);
    var hintEl = $('[data-restart-hint]', box);

    var seconds = parseInt(box.getAttribute('data-restart-seconds') || '5', 10);
    if (isNaN(seconds) || seconds < 0) seconds = 5;

    var POLL_INTERVAL = 1000;
    var MAX_POLLS = 120; // 最多等约 2 分钟，避免异常时无限轮询

    var timer = null;
    var polls = 0;
    var finished = false;

    function setHint(text) {
      if (hintEl) hintEl.textContent = text;
    }

    function finish() {
      if (finished) return;
      finished = true;
      if (timer) clearInterval(timer);
      window.location.replace(target);
    }

    // 服务尚未起来时 fetch 会直接 reject（连接被拒），静默重试即可
    function probe() {
      fetch(probeURL, { cache: 'no-store', credentials: 'same-origin' })
        .then(function (res) { if (res.ok) finish(); })
        .catch(function () { /* 还没就绪，等下一轮 */ });
    }

    function startWaiting() {
      setHint('服务正在重启，正在等待它重新就绪……');
      probe();
      timer = setInterval(function () {
        polls += 1;
        if (polls > MAX_POLLS) {
          clearInterval(timer);
          setHint('等待时间超出预期，请点击下方按钮手动刷新。');
          return;
        }
        probe();
      }, POLL_INTERVAL);
    }

    // 倒计时期间绝不能探测：重启前有约 1.2 秒的延迟（handlers.restartDelay），
    // 旧进程在这段时间里仍在正常响应 /readyz。过早探测会在旧进程上
    // 「刷新成功」，用户看到的还是旧版本 / 恢复前的数据。
    timer = setInterval(function () {
      seconds -= 1;
      if (countdownEl) countdownEl.textContent = String(Math.max(seconds, 0));
      if (seconds <= 0) {
        clearInterval(timer);
        timer = null;
        startWaiting();
      }
    }, 1000);
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
    initFlashDismiss();
    initHistoryBack();
    initRestartWatcher();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
