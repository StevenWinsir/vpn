package com.asterlink.vpn.ui.login

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.autofill.ContentType
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentType
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.asterlink.vpn.R
import com.asterlink.vpn.session.AppPhase
import com.asterlink.vpn.session.SessionState
import com.asterlink.vpn.ui.AppActions
import com.asterlink.vpn.ui.BuildFacts
import com.asterlink.vpn.ui.WebPage
import com.asterlink.vpn.ui.components.AsterIcon
import com.asterlink.vpn.ui.components.BrandMark
import com.asterlink.vpn.ui.components.NoticeBanner
import com.asterlink.vpn.ui.components.PrimaryButton
import com.asterlink.vpn.ui.components.noticeText
import com.asterlink.vpn.ui.theme.Dimens
import com.asterlink.vpn.ui.theme.TabularNumbers

/**
 * The entry screen: email, password (visibility toggle), "remember password", sign in, and the
 * website for registration. Errors appear under the fields and are read out. The fields are
 * local UI state; the password never leaves this composable except into one sign-in call.
 */
@Composable
fun LoginScreen(state: SessionState, facts: BuildFacts, actions: AppActions, modifier: Modifier = Modifier) {
    var email by rememberSaveable(state.email) { mutableStateOf(state.email) }
    // Not saveable: a password must not be written into the saved instance state.
    var password by androidx.compose.runtime.remember { mutableStateOf("") }
    var remember by rememberSaveable(state.rememberPassword) { mutableStateOf(state.rememberPassword) }
    var visible by rememberSaveable { mutableStateOf(false) }
    val focus = LocalFocusManager.current
    val signingIn = state.phase == AppPhase.SigningIn || state.phase == AppPhase.Starting
    val submit = {
        focus.clearFocus()
        actions.signIn(email, password, remember)
    }

    Surface(modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Box(Modifier.fillMaxSize().safeDrawingPadding().imePadding(), contentAlignment = Alignment.TopCenter) {
            Column(
                Modifier
                    .widthIn(max = 480.dp)
                    .fillMaxWidth()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = Dimens.PageMargin, vertical = 24.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Spacer(Modifier.height(32.dp))
                BrandMark()
                Spacer(Modifier.height(16.dp))
                Text(stringResource(R.string.login_title), style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
                Spacer(Modifier.height(4.dp))
                Text(
                    stringResource(R.string.login_subtitle),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
                Spacer(Modifier.height(28.dp))

                state.coreProblem?.let {
                    NoticeBanner(noticeText(it))
                    Spacer(Modifier.height(16.dp))
                }

                OutlinedTextField(
                    value = email,
                    onValueChange = { email = it },
                    label = { Text(stringResource(R.string.login_email)) },
                    singleLine = true,
                    enabled = !signingIn,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email, imeAction = ImeAction.Next, autoCorrectEnabled = false),
                    modifier = Modifier.fillMaxWidth().semantics { contentType = ContentType.EmailAddress + ContentType.Username },
                )
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = password,
                    onValueChange = { password = it },
                    label = { Text(stringResource(R.string.login_password)) },
                    singleLine = true,
                    enabled = !signingIn,
                    visualTransformation = if (visible) VisualTransformation.None else PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done, autoCorrectEnabled = false),
                    keyboardActions = KeyboardActions(onDone = { submit() }),
                    trailingIcon = {
                        IconButton(onClick = { visible = !visible }) {
                            AsterIcon(
                                if (visible) R.drawable.ic_visibility_off else R.drawable.ic_visibility,
                                stringResource(if (visible) R.string.login_hide_password else R.string.login_show_password),
                            )
                        }
                    },
                    isError = state.message != null,
                    modifier = Modifier.fillMaxWidth().semantics { contentType = ContentType.Password },
                )
                state.message?.let {
                    Text(
                        noticeText(it),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.fillMaxWidth().padding(top = 8.dp, start = 4.dp).semantics { liveRegion = LiveRegionMode.Polite },
                    )
                }
                Spacer(Modifier.height(12.dp))
                // The whole row is the switch: a tap on the label toggles it, and TalkBack reads one
                // item ("Remember password, switch, on") instead of a label and an unnamed switch.
                Row(
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = 48.dp)
                        .toggleable(value = remember, enabled = !signingIn, role = Role.Switch, onValueChange = { remember = it }),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(stringResource(R.string.login_remember), style = MaterialTheme.typography.bodyMedium)
                        Text(
                            stringResource(R.string.login_remember_hint),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    Spacer(Modifier.padding(start = 12.dp))
                    Switch(checked = remember, onCheckedChange = null, enabled = !signingIn)
                }
                state.warning?.let {
                    Spacer(Modifier.height(12.dp))
                    NoticeBanner(noticeText(it), error = false)
                }
                Spacer(Modifier.height(24.dp))
                PrimaryButton(
                    text = stringResource(if (state.phase == AppPhase.SigningIn) R.string.login_signing_in else R.string.login_submit),
                    onClick = submit,
                    loading = state.phase == AppPhase.SigningIn,
                    enabled = !signingIn,
                )
                Spacer(Modifier.height(8.dp))
                Row(horizontalArrangement = Arrangement.Center, modifier = Modifier.fillMaxWidth()) {
                    TextButton(onClick = { actions.openWebsite(WebPage.Register) }) { Text(stringResource(R.string.login_register)) }
                    TextButton(onClick = { actions.openWebsite(WebPage.ForgotPassword) }) { Text(stringResource(R.string.login_forgot_password)) }
                }
                Row(horizontalArrangement = Arrangement.Center, modifier = Modifier.fillMaxWidth()) {
                    TextButton(onClick = { actions.openWebsite(WebPage.Home) }) { Text(stringResource(R.string.login_website)) }
                }
                Spacer(Modifier.height(32.dp))
                Footer(facts)
            }
        }
    }
}

@Composable
private fun Footer(facts: BuildFacts) {
    val caption = MaterialTheme.typography.bodySmall.merge(TabularNumbers)
    val color = MaterialTheme.colorScheme.onSurfaceVariant
    Text(
        stringResource(R.string.login_footer, facts.appVersion),
        style = caption,
        color = color,
        textAlign = TextAlign.Center,
    )
    if (facts.channel != "release") {
        Text(stringResource(R.string.login_dev_channel), style = caption, color = color, textAlign = TextAlign.Center)
    }
}
