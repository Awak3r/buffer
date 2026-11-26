package ru.buffer;

import android.Manifest;
import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.hardware.Camera;
import android.os.Bundle;
import android.view.*;
import android.widget.Toast;
import com.google.zxing.*;
import com.google.zxing.common.HybridBinarizer;
import com.google.zxing.qrcode.QRCodeReader;

@SuppressWarnings("deprecation")
public class Scan extends Activity implements SurfaceHolder.Callback, Camera.PreviewCallback {
    private Camera camera;
    private SurfaceView preview;
    private boolean resumed;
    private final QRCodeReader reader = new QRCodeReader();
    @Override public void onCreate(Bundle b) {
        super.onCreate(b);
        preview = new SurfaceView(this); preview.getHolder().addCallback(this); setContentView(preview);
        setTitle("Наведи камеру на QR");
        if (checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED)
            requestPermissions(new String[]{Manifest.permission.CAMERA}, 1);
    }
    @Override public void onRequestPermissionsResult(int req,String[] perms,int[] results) {
        super.onRequestPermissionsResult(req,perms,results);
        if (results.length == 0 || results[0] != PackageManager.PERMISSION_GRANTED) {
            Toast.makeText(this,"Для сканирования нужен доступ к камере",Toast.LENGTH_LONG).show(); finish();
        } else start();
    }
    @Override public void onResume() { super.onResume(); resumed = true; start(); }
    @Override public void onPause() { resumed = false; stop(); super.onPause(); }
    public void surfaceCreated(SurfaceHolder h) { start(); }
    public void surfaceChanged(SurfaceHolder h,int fmt,int w,int height) { start(); }
    public void surfaceDestroyed(SurfaceHolder h) { stop(); }
    private void start() {
        if (!resumed || camera != null || !preview.getHolder().getSurface().isValid() ||
            checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) return;
        try {
            camera = Camera.open();
            Camera.Parameters p = camera.getParameters();
            if (p.getSupportedFocusModes().contains(Camera.Parameters.FOCUS_MODE_CONTINUOUS_PICTURE))
                p.setFocusMode(Camera.Parameters.FOCUS_MODE_CONTINUOUS_PICTURE);
            camera.setParameters(p); camera.setDisplayOrientation(90);
            camera.setPreviewDisplay(preview.getHolder()); camera.startPreview(); camera.setOneShotPreviewCallback(this);
        } catch (Exception e) { stop(); Toast.makeText(this,"Камера недоступна",Toast.LENGTH_LONG).show(); finish(); }
    }
    private void stop() {
        if (camera != null) { camera.setPreviewCallback(null); camera.release(); camera = null; }
    }
    public void onPreviewFrame(byte[] data, Camera source) {
        if (camera != source) return;
        Camera.Size s = source.getParameters().getPreviewSize();
        try {
            LuminanceSource pixels = new PlanarYUVLuminanceSource(data,s.width,s.height,0,0,s.width,s.height,false);
            Result result = reader.decode(new BinaryBitmap(new HybridBinarizer(pixels)));
            setResult(RESULT_OK,new Intent().putExtra("qr",result.getText())); finish();
        } catch (ReaderException e) {
            preview.postDelayed(() -> { if (camera == source) camera.setOneShotPreviewCallback(this); },100);
        } finally { reader.reset(); }
    }
}
