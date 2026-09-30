package dev.lanchat

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.core.content.ContextCompat
import androidx.lifecycle.viewmodel.compose.viewModel
import dev.lanchat.service.CoreService
import dev.lanchat.ui.AppViewModel
import dev.lanchat.ui.screen.ChatScreen
import dev.lanchat.ui.screen.LoginScreen
import dev.lanchat.ui.screen.MainScreen
import dev.lanchat.ui.theme.LANChatTheme

class MainActivity : ComponentActivity() {

    private val notificationPermission =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        CoreService.start(this)
        requestNotificationPermission()
        setContent {
            LANChatTheme {
                val vm: AppViewModel = viewModel()
                val state by vm.ui.collectAsState()
                if (state.conn == "connected") {
                    MainScreen(state, vm)
                } else {
                    LoginScreen(state, vm)
                }
            }
        }
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            val granted = ContextCompat.checkSelfPermission(
                this,
                Manifest.permission.POST_NOTIFICATIONS,
            ) == PackageManager.PERMISSION_GRANTED
            if (!granted) notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }
}
