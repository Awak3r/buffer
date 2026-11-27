package ru.buffer;

import android.app.Activity;
import android.app.KeyguardManager;

import android.os.Build;
import android.os.Bundle;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

public final class QuickTransfer extends Activity {
    static final String SEND = "ru.buffer.QUICK_SEND";
    static final String PULL = "ru.buffer.QUICK_PULL";

    private TextView status;
    private Button close;
    private boolean started;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        setFinishOnTouchOutside(false);
        LinearLayout view = new LinearLayout(this);
        view.setOrientation(LinearLayout.VERTICAL);
        int pad = (int)(20 * getResources().getDisplayMetrics().density);
        view.setPadding(pad, pad, pad, pad);
        status = new TextView(this);
        status.setTextSize(18);
        view.addView(status);
        close = new Button(this);
        close.setText("Закрыть");
        close.setOnClickListener(v -> finish());
        view.addView(close);
        setContentView(view);
        if (state != null) {
            started = state.getBoolean("started");
            status.setText(state.getString("status"));
        }
        close.setVisibility(started ? View.VISIBLE : View.GONE);
    }

    @Override public void onSaveInstanceState(Bundle state) {
        super.onSaveInstanceState(state);
        state.putBoolean("started", started);
        state.putString("status", close.getVisibility() == View.VISIBLE ? status.getText().toString()
            : "Окно изменилось. Проверь буфер перед повтором.");
    }

    @Override public void onWindowFocusChanged(boolean focused) {
        super.onWindowFocusChanged(focused);
        // Android grants clipboard access to the focused window, not the tile service.
        if (!focused || started || isFinishing()) return;
        started = true;
        if (getSystemService(KeyguardManager.class).isDeviceLocked()) {
            finish();
            return;
        }
        String action = getIntent().getAction();
        if (!SEND.equals(action) && !PULL.equals(action)) {
            finish();
            return;
        }
        if (!Pair.file(this).getBaseFile().exists()) {
            showError("Открой «На компьютер» и привяжи компьютер по QR.");
            return;
        }

        boolean pulling = PULL.equals(action);
        String value = pulling ? null : Transfer.readClipboard(this);
        if (!pulling && value == null) {
            showError("В буфере телефона нет текста или ссылки.");
            return;
        }
        if (!Transfer.valid(value)) {
            showError(Transfer.INVALID_TEXT);
            return;
        }
        status.setText(Transfer.progress(value));
        Transfer.run(this, value, result -> {
            if (!result.success) {
                showError(result.message);
                return;
            }
            if (!pulling || Build.VERSION.SDK_INT < 33) {
                Toast.makeText(this, result.message, Toast.LENGTH_SHORT).show();
            }
            finish();
        });
    }

    private void showError(String message) {
        status.setText(message);
        close.setVisibility(View.VISIBLE);
    }
}
