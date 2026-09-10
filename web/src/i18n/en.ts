// Approved landing spec, with PM copy rulings. HTML is authored here, never user input.
export const en = {
  app_job_daily_exhausted: "The host's images for today are used up. Try again tomorrow.",
  app_job_meter_uncapped: "{used} images today",
  app_privacy_images: "Pictures you ask for, and the words you asked with, stay on the host under your key for {days} days.",
  app_close: "Close",
  app_job_attachments: "Images are made from text. Remove the attachments, or switch back to chat.",
  app_job_uncertain: "The host has not confirmed these images. They were not sent again.",
  app_job_meter: "{used}/{limit} images today",

  app_job_make: "Make an image",
  app_job_placeholder: "Describe an image…",
  app_job_prompts_line: "{prompts} prompts · {images} images",
  app_job_cancelling: "cancelling · after this image · {elapsed}",
  app_job_cancelled_unstarted: "cancelled · not started",
  app_job_failed: "{host} could not make this image. Its own words are under Details.",
  app_job_refused: "{host} keeps {n} of your images in line at most. Wait for one to finish, then ask again.",
  app_job_meta_failed: "{elapsed} on {host}",
  app_job_output_gone: "no longer on {host}",
  app_job_save: "Save",
  app_job_discard: "Discard",
  app_job_edit_prompt: "Edit prompt",
  app_job_list: "Images",
  app_job_list_caption: "{count} images on {host} · kept {days} days",
  app_job_group: "{when} · {count} images",
  app_job_output_line: "{w} × {h} · {size} · {elapsed} · {days} days left",
  app_job_list_empty: "No images yet. Press the picture square by the field and describe one.",
  app_settings_images: ", makes images with {model}",
  app_limits_jobs: "An image is made on {host} one at a time, in the order it was asked for; chats keep going, a little slower while an image is being made. The host counts the images each key makes in a day and decides how many it will hold in line for you. A finished image stays on the host {days} days, in Images, for you to save.",
  app_job_prompts_line_one: "1 prompt · 1 image",
  app_run_meta_images_one: "1 image · {elapsed} on {host}",

  app_run_queued_unknown: "queued",
  app_run_submitting: "sending to {host}",
  app_run_uncertain: "The host has not confirmed this run. It was not sent again.",

  app_run_queued: "queued · {ordinal} in line",
  app_run_queued_next: "queued · next in line",
  app_run_generating: "generating · {elapsed}",
  app_run_step: "{step} · {elapsed}",
  app_run_steps_so_far: "{count} steps so far",
  app_run_steps: "{count} steps",
  app_run_step_line: "{elapsed} {step}",
  app_run_step_result: "{step} → {result}",
  app_run_result_search: "{count} results",
  app_run_result_run: "exit {code} · {tail}",
  app_run_result_lines: "{count} lines",
  app_run_result_failed: "failed",
  app_run_step_search: "searching the web · {tool}",
  app_run_step_run: "running {name}",
  app_run_step_read: "reading {name}",
  app_run_step_write: "writing {name}",
  app_run_step_think: "thinking",
  app_run_waiting: "waiting for you",
  app_run_ask: "The agent asks for permission before this step:",
  app_run_allow: "Allow",
  app_run_deny: "Deny",
  app_run_cancelled: "cancelled · {elapsed}",
  app_run_failed: "This run stopped on {host}. Its own words are under Details.",
  app_run_refused: "{host} refused this run. Its own words are under Details.",
  app_run_meta_agent: "{elapsed} on {host} · {input} tokens in · {output} out",
  app_run_meta_images: "{count} images · {elapsed} on {host}",
  app_run_output_n_of: "Output {n} of {count}: {name}",
  app_run_steps_one: "1 step",
  app_run_steps_so_far_one: "1 step so far",
  app_run_output_caption: "{kind} · {extent} · made on {host}",
  app_run_output_image_caption: "{w} × {h} · {size} · made on {host}",
  app_limits_runs: "A run is work {host} does for you after you ask — an image, or an agent working in its own sandbox on that machine — and it counts in its own way: the host counts the seconds it runs and the tokens it spends, and decides how many runs you can queue. Each run’s own line shows what it took once the host has counted.",

  app_voice_waveform: "Microphone waveform",
  app_admin_title: "Remote console",
  app_admin_code: "Admin code",
  app_admin_trust: "Anyone with this admin code controls the host’s keys. It is kept only until this page closes.",
  app_admin_expected: "Paste an admin code beginning with ia1.",
  app_open_console: "Open console",
  app_forget_console: "Forget this console",
  app_admin_hint: "Reads as an admin code · opens the console of host {host}",
  app_admin_timeout: "The host did not answer. Try opening the console again.",
  app_admin_direct: "direct · {ms} ms",
  app_admin_relay: "relayed via {place} · {ms} ms",
  app_admin_path_unknown: "path not reported",
  app_mic: "Speak",
  app_mic_stop: "Stop recording",
  app_type_or_speak: "Type or speak…",
  app_voice_waiting: "Waiting for the microphone…",
  app_voice_cancel: "Cancel",
  app_voice_recording: "{m}:{ss}",
  app_voice_transcribing: "{seconds} s · transcribing on {host}…",
  app_voice_transcribed: "{seconds} s · {count} words · transcribed on {host}",
  app_voice_failed: "{title} — {seconds} s of audio was not transcribed and was not kept.",
  app_mic_blocked: "Microphone blocked by the browser. Allow it for this site, then tap the mic again.",
  app_no_microphone: "No microphone on this device.",
  app_listen: "Listen",
  app_speech_making: "making speech on {host}…",
  app_speech_playing: "{position} / {duration} · {count} characters · made on {host}",
  app_speech_refused: "No speech for this reply: {host} refused it. Its own words are under Details.",
  app_speech_failed: "No speech for this reply: {host} couldn’t make it. Its own words are under Details.",
  app_settings_hears: "Hears speech: {model}.",
  app_settings_speaks: "Speaks replies: {model}.",
  app_voice_transcribed_one: "{seconds} s · 1 word · transcribed on {host}",
  app_limits_voice: "Voice counts too. Speech you record is sent to {host} as audio and comes back as text; a reply you listen to is made into speech there from its words. Audio has its own daily budgets, counted by the host and shown in its console. The web app shows the seconds recorded and characters spoken; audio does not move the daily tokens meter.",

  app_attach: "Attach an image or a file",
  app_attach_file: "Attach a file",
  app_hint_enter_sends_attach: "Enter sends · Shift+Enter makes a new line · paste or drop an image or a file",
  app_hint_enter_sends_files: "Enter sends · Shift+Enter makes a new line · paste or drop a file",
  app_limits_files: "A file is sent as its text. The web app takes the words out on your device — a PDF’s text layer, a Word file’s paragraphs, code as it is — and they count like typed text, so the meter already includes them. Every file in this chat is sent again with each question.",

  app_attachment_reading: 'reading…',
  app_image_storage_failed: 'The host could not store this image; it did not count.',
  app_image_storage_retry: 'Try again. If it keeps failing, the host’s disk needs attention.',
  app_image_limit: 'Up to 4 images per message.',
  app_attach_an_image: "Attach an image",
  app_remove_image: "Remove image",
  app_images_line: "{count} images · {px} px · {size}",
  app_hint_enter_sends_images: "Enter sends · Shift+Enter makes a new line · paste or drop an image",
  app_drop_to_attach: "Drop to attach",
  app_model_cant_see_images: "{model} can’t see images — text only.",
  app_could_not_read_image_kind: "Couldn’t read this {kind} image.",
  app_could_not_read_that_image: "Couldn’t read that image.",
  app_over_message_size: "Over the {size} one message can carry — remove an image.",
  app_meter_context_images: "{used}/{limit} context + {count} images",
  app_meter_context_unknown_images: "— / {limit} context + {count} images",
  app_message_usage_images: " · {input} tokens in ({count} images) · {output} out",
  app_limits_images: "An image costs tokens too. How many is the model’s decision, so the meter counts the text and names the images it is not counting. Every image carried with its turn is sent again with each question, so a chat with images fills the memory sooner.",
  app_engine_rejected_with_images: "The engine on {host} rejected this question. It carried {count} images; the engine’s own words are under Details.",
  app_sent_image_caption: "{w} × {h} · {size} · as sent to {host}",
  app_settings_vision: ", sees images",
  app_image_n_of: "Image {n} of {count}",
  app_image_missing: "Not sent — this image is no longer on this device.",

  s0_eye: '00 · See it work',
  s0_try: "Try it on our demo host<span aria-hidden=\"true\">→</span>",
  s0_host: "<span>host <b>Max’s workstation</b></span><wbr><span> · Qwen3.8 27B</span><wbr><span> · 2 × RTX PRO 6000</span>",
  s0_note: "Shared with everyone reading this page, 30 at a time. If it says the invite is busy, try again in a moment.",
  s0_qr: "QR code for infercat.ai/try",
  demo_caption: 'Watch the demo',
  demo_label: 'Play the demo',
  nav_host: 'Host your own',
  nav_src: 'Source',
  h_display: 'Chat with a friend’s GPU.',
  h_lead: 'They send you one code; you paste it here. No account, no install, nothing to set up.',
  h_promise:
    'Encrypted end-to-end from your device to your host’s computer — the relay in between can’t read it. Infercat records counts, never text. The model runs on their machine.',
  h_facts: 'Self-hosted · end-to-end encrypted · MIT',
  h_pitch:
    'Chat with a friend’s GPU. They send you one code; you paste it here. No account, no install, nothing to set up.',
  f_label: 'Invite code',
  f_paste: 'Paste',
  f_hint: '<span class="ck">✓</span> Reads as an invite · host <span class="host">tco2Fw…</span>',
  f_connect: 'Connect',
  f_quiet:
    'No code? Ask a friend who runs Infercat, or <span class="nb"><a href="#">host your own</a> <span aria-hidden="true">→</span></span>',
  h_down: 'How it works ↓',
  idx: '00 See it work · 01 How it works · 02 The invite · 03 For developers · 04 Roadmap',
  s1_eye: '01 · How it works',
  s1_h: 'Your machine runs the model. Your friends get a key.',
  s1_lead:
    'One program sits in front of your engine. Each friend gets one invite. Everything between you is encrypted end to end.',
  s1_mut: 'Press a box for details. Switch the path to see where traffic goes.',
  node_f: 'A friend',
  node_f_s: 'browser or terminal',
  node_r: 'Relay',
  node_r_s: 'meeting point',
  node_h: 'Your machine',
  node_h_s: 'engine and keys',
  brk_l: 'direct',
  pop_f:
    '<p>Pastes the code into the web app. Any browser, nothing to install. The app carries the tunnel itself, as WebAssembly.</p><p>Or runs <code>infercat connect</code> in a terminal and points any OpenAI-style app at it.</p>',
  pop_r:
    '<p>The two machines meet here. It passes on packets that are already encrypted. It holds no key, so it cannot read them. (WireGuard, over a DERP relay.)</p><p>Browsers always use it today. Run your own with <code>serve --derpmap-url</code>.</p>',
  pop_h:
    '<p><code>infercat serve</code> finds your engine (llama.cpp, llama-swap, vLLM, Ollama or LM Studio) and serves it inside the tunnel. Nothing else is reachable: no other port, no files.</p><p>It records one line per request: key, endpoint, status, token counts, timings. These request logs never include text unless you run <code>--log-prompts</code>. The app tells your friend if you do.</p><p>Hosts with an image engine also keep requested pictures and their prompts under your key until they expire.</p>',
  close: 'close',
  path_r: 'relayed',
  path_d: 'direct',
  path_r_d: 'The web app today: every packet crosses the relay encrypted. The relay cannot open it.',
  path_d_d:
    '<code>infercat connect</code>, when the networks allow: the two machines talk to each other. The relay only introduced them.',
  fact1_k: 'The relay sees',
  fact1_v: 'sealed packets, never a word.',
  fact2_k: 'Your machine records',
  fact2_v: 'counts, never text.',
  fact3_k: 'The relay can be',
  fact3_v: 'your own. One flag.',
  s2_eye: '02 · The invite',
  s2_h: 'One code, two keys.',
  s2_lead: "The tunnel address finds the host. The gateway key says who you are and how much you may use.",
  s2_mut:
    'Your machine makes one code per friend and keeps only a hash of the secret. Revoke one friend and only their code stops.',
  seg_v: 'version',
  seg_a: "Tunnel address",
  seg_s: "Gateway key",
  tt_v: '<b>Format version.</b> If a code needs a newer app, the app says so.',
  tt_a: "Where the host is, from the tunnel library. It carries the host's public key, so your tunnel can only end at their machine. Same for every friend; it also carries a shared secret, so keep the whole invite private.",
  tt_s: "Your API key for the host's gateway, like OPENAI_API_KEY. The app sends it with every request; the host keeps only a hash. Made when the host adds you.",
  verbs: 'keys add · limits · pause · rotate · revoke · applied at once, no restart',
  lim_sum: 'Limits a host can set',
  d1_l: 'requests a minute',
  d2_l: 'tokens a minute',
  d3_l: 'request at a time',
  d4_l: 'tokens a day',
  d5_v: 'the engine’s limit',
  d5_l: 'context per request',
  d6_v: 'all',
  d6_l: 'models',
  lim_note:
    'Defaults for a new key, plus 4 096 output tokens per request. Change any of them at any time. Over a limit a friend gets 429 with Retry-After. When the engine is full they wait briefly, then get 503.',
  s3_eye: '03 · For developers',
  s3_h: 'Or skip the browser.',
  s3_lead:
    '<code>infercat connect</code> turns the same code into a local endpoint for any OpenAI-style app.',
  s3_mut:
    "Open WebUI, Cursor, Claude Code, the SDKs or plain curl. Your friend’s model, as if it ran on your machine.<br>Hosts: use <code>infercat expose</code> to give your host a public URL.",
  reach_sum: 'What your app can reach',
  reach_b:
    '<p><code>/v1/chat/completions</code> · <code>/v1/responses</code> · <code>/v1/models</code> · <code>/v1/embeddings</code> · <code>/me</code>. Nothing else: no other port, no files.</p><p><code>connect</code> adds the invite’s key to every request and ignores your app’s own. Over a limit: <code>429</code> with <code>Retry-After</code>. Host busy: a short wait, then <code>503</code>. Host asleep: <code>connect</code> says so and reconnects on its own.</p>',
  s4_eye: '04 · Roadmap',
  s4_tag: "Preview",
  s4_h: 'A public URL for your host.',
  s4_price: "Free during preview: <code>gateway.infercat.ai/h/&lt;host&gt;/v1</code> connects any OpenAI-style app to your host.",
  s4_lead: "a public URL decrypts TLS at the edge and in our object; the tunnel mode's \"nobody in the middle\" does not carry over.",
  s4_mut:
    "The code in the middle is open in <a href=\"https://github.com/infercat/infercat/tree/main/bridge\"><code>bridge/</code> in the Infercat repository</a>.",
  rm_c: 'any client',
  rm_g: 'public gateway',
  rm_g_s: "sees requests",
  rm_h: 'your machine',
  ml_label: 'Email',
  ml_ph: 'you@example.com',
  ml_btn: "I’d like to keep this after the preview",
  ml_note: "Your address, nothing else, on our own server. We’ll email you before the preview ends.",
  ml_ok_t: 'You’re on the list.',
  ml_ok_b: "We’ll email you before the preview ends. Nothing else.",

  ft_about:
    'Built on tailcat, Tailscale’s open-source library. Infercat is not affiliated with or endorsed by Tailscale Inc.',
  ml_invalid: "That doesn't look like an email address.",
  ml_failed: "Couldn't save it. Try again in a moment.",
  ml_limited: 'Too many tries from this network. Try again later.',
  version: 'Version',
  source: 'Source on GitHub',
  about: 'About',
  made: 'Made by 2185 Lab',
  path_label: 'Path',
  roadmap_label: "any client, a public gateway that sees requests, your machine",
  terminal:
    '$ infercat connect {invite}\nInfercat 0.1.0\nhost    Max’s laptop · gemma-4-E2B-it-Q4_K_M.gguf\npath    relayed via New York City · 27 ms\nlocal   http://127.0.0.1:11435\n        base URL http://127.0.0.1:11435/v1, any API key\n\n$ export OPENAI_BASE_URL=http://127.0.0.1:11435/v1 OPENAI_API_KEY=x\n$ python3 chat.py',
  f_hint_empty: 'Paste the code your friend sent you.',

  // 047 app copy. Named placeholders are substituted as text, never HTML.
  // ui/Chat.tsx:92 — VariableDeclaration
  app_another_tab_took_over_this_chat_what_is_above: "Another tab took over this chat — what is above is only part of it.",
  // ui/Chat.tsx:288 — PropertyAssignment
  app_no_model_available: "No model available",
  // ui/Chat.tsx:289 — PropertyAssignment
  app_this_host_has_not_shared_a_model_with_your: "This host has not shared a model with your invite.",
  // ui/Chat.tsx:448 — PropertyAssignment; ui/Connect.tsx:455
  app_reconnect: "Reconnect",
  // ui/Chat.tsx:450 — ConditionalExpression; ui/Chat.tsx:611; ui/Connect.tsx:483
  app_try_again: "Try again",
  // ui/Chat.tsx:451 — PropertyAssignment
  app_regenerate: "Regenerate",
  // ui/Chat.tsx:461 — JsxElement
  app_new_chat: "New chat",
  // ui/Chat.tsx:492 — JsxElement
  app_disconnect: "Disconnect",
  // ui/Chat.tsx:515 — JsxElement; ui/Chat.tsx:888
  app_settings: "Settings",
  // ui/Chat.tsx:524 — JsxElement; ui/Connect.tsx:479
  app_paste_a_new_code: "Paste a new code",
  // ui/Chat.tsx:531 — JsxElement
  app_this_chat_is_open_in_another_tab: "This chat is open in another tab.",
  // ui/Chat.tsx:533 — JsxElement
  app_use_this_tab_instead: "Use this tab instead",
  // ui/Chat.tsx:539 — JsxElement
  app_this_host_records_prompts_and_replies_to_a_log: "This host records prompts and replies to a log on its machine.",
  // ui/Chat.tsx:576 — onContinue
  app_continue_from_where_you_stopped: "Continue from where you stopped.",
  // ui/Chat.tsx:587 — JsxElement
  app_chat_deleted: "Chat deleted",
  // ui/Chat.tsx:598 — JsxElement
  app_undo: "Undo",
  // ui/Chat.tsx:621 — JsxElement
  app_dismiss: "Dismiss",
  // ui/Chat.tsx:636 — hint
  app_send_will_work_again_the_moment_your_host_resumes: "Send will work again the moment your host resumes your invite.",
  // ui/Chat.tsx:639 — hint
  app_enter_sends_shift_enter_makes_a_new_line: "Enter sends · Shift+Enter makes a new line",
  // ui/Chat.tsx:676 — aria-label
  app_what_these_limits_mean: "What these limits mean",
  // ui/Chat.tsx:700 — JsxElement
  app_your_limits: "Your limits",
  // ui/Chat.tsx:702 — JsxElement
  app_your_host_caps_how_fast_one_invite_can_send: "Your host caps how fast one invite can send, so a burst from you never stalls their machine for everyone else.",
  // ui/Chat.tsx:706 — JsxElement
  app_a_token_is_roughly_three_quarters_of_a_word: "A token is roughly three quarters of a word, counting both what you write and what the model answers. The count resets daily.",
  // ui/Chat.tsx:711 — JsxElement
  app_the_model_s_memory_the_meter_is_what_the: "The model’s memory. The meter is what the next message will carry — the chat so far, minus thinking, minus anything left out to fit. As it fills, replies get shorter; a message that alone is too long for the memory is left out of the next question; a new chat starts empty.",
  // ui/Chat.tsx:722 — ConditionalExpression
  app_every_reply_so_far_waited_for_a_slot_first: "Every reply so far waited for a slot first",
  // ui/Chat.tsx:730 — JsxElement
  app_a_reply_that_waited_for_a_free_slot_says: "A reply that waited for a free slot says so on the reply and is left out of the first-token median.",
  // ui/Chat.tsx:755 — ArrayLiteralExpression
  app_how_can_you_answer_me_if_you_are_running: "How can you answer me if you are running on someone else’s computer?",
  // ui/Chat.tsx:756 — ArrayLiteralExpression
  app_write_a_haiku_about_borrowing_a_stranger_s_gpu: "Write a haiku about borrowing a stranger’s GPU.",
  // ui/Chat.tsx:757 — ArrayLiteralExpression
  app_what_can_you_help_me_with: "What can you help me with?",
  // ui/Chat.tsx:768 — ConditionalExpression
  app_waiting_for_a_model: "Waiting for a model.",
  app_waiting_for_model_load: "Waiting for {host} to load {model}…",
  // ui/Chat.tsx:830 — aria-label
  app_message: "Message",
  // ui/Chat.tsx:831 — placeholder
  app_message_the_host_s_model: "Message the host’s model…",
  // ui/Chat.tsx:845 — JsxElement
  app_stop: "Stop",
  // ui/Chat.tsx:856 — JsxElement
  app_send: "Send",
  // ui/Chat.tsx:890 — JsxElement
  app_model: "Model",
  // ui/Chat.tsx:900 — JsxElement
  app_system_prompt: "System prompt",
  // ui/Chat.tsx:904 — placeholder
  app_optional_sent_ahead_of_every_message_in_this_browser: "Optional. Sent ahead of every message in this browser.",
  // ui/Chat.tsx:918 — JsxElement
  app_lower_is_more_predictable_higher_is_more_surprising: "Lower is more predictable, higher is more surprising.",
  // ui/Chat.tsx:923 — JsxElement; ui/Message.tsx:255
  app_thinking: "Thinking",
  // ui/Chat.tsx:926 — JsxElement
  app_model_default: "Model default",
  // ui/Chat.tsx:927 — JsxElement
  app_on_better_answers_on_hard_questions: "On — better answers on hard questions",
  // ui/Chat.tsx:928 — JsxElement
  app_off_faster_shorter_fewer_of_your_tokens: "Off — faster, shorter, fewer of your tokens",
  // ui/Chat.tsx:931 — JsxElement
  app_the_switch_appears_once_the_model_has_shown_its: "The switch appears once the model has shown its thinking in this chat.",
  // ui/Chat.tsx:941 — ConditionalExpression
  app_not_answering_right_now: " — not answering right now",
  // Settings: fallback label when no model is selected; this is not a model id.
  app_none: "none",
  // ui/Chat.tsx:944 — ConditionalExpression
  app_another_tab_of_this_browser_holds_the_saved_tunnel: " Another tab of this browser holds the saved tunnel identity, so this tab connected as a second client.",
  // ui/Chat.tsx:950 — JsxElement
  app_app_version: "App version",
  // ui/Chat.tsx:954 — JsxElement; ui/Connect.tsx:356; ui/Message.tsx:67
  app_cancel: "Cancel",
  // ui/Chat.tsx:963 — JsxElement
  app_done: "Done",
  // ui/Connect.tsx:65 — PropertyAssignment
  app_loading_the_tunnel: "Loading the tunnel",
  // ui/Connect.tsx:66 — PropertyAssignment
  app_connecting_to_the_relay: "Connecting to the relay",
  // ui/Connect.tsx:67 — PropertyAssignment
  app_checking_your_invite: "Checking your invite",
  // ui/Connect.tsx:298 — JsxElement
  app_forget_removes_the_code_and_this_device_s_tunnel: "Forget removes the code and this device’s tunnel identity. Your chats stay.",
  // ui/Connect.tsx:369 — JsxElement
  app_welcome_back: "Welcome back.",
  // ui/Connect.tsx:387 — JsxElement
  app_invite_from_your_link_is_ready: "Invite from your link is ready.",
  // ui/Connect.tsx:396 — JsxElement
  app_show: "Show",
  // ui/Connect.tsx:460 — JsxElement; ui/Connect.tsx:488
  app_forget_this_invite: "Forget this invite",
  // ui/Connect.tsx:472 — JsxElement; ui/Message.tsx:215
  app_details: "Details",
  // ui/Connect.tsx:675 — JsxElement
  app_this_host_is_running_with_prompt_logging_on_everything: "This host is running with prompt logging on. Everything you send, and everything the model answers, is written to a log on their machine. That is not the normal setting and it is not something this app can turn off.",
  // ui/Connect.tsx:684 — JsxElement
  app_i_understand_start_chatting: "I understand — start chatting",
  // ui/Connect.tsx:687 — JsxElement
  app_not_now: "Not now",
  // ui/Connect.tsx:696 — ReturnStatement
  app_done_lowercase: "done",
  // ui/Connect.tsx:716 — PropertyAssignment
  app_could_not_load_the_tunnel: "Could not load the tunnel",
  // ui/Connect.tsx:717 — PropertyAssignment
  app_reload_the_page_if_it_keeps_failing_this_copy: "Reload the page; if it keeps failing, this copy of the app was published without its tunnel module.",
  // ui/Message.tsx:71 — JsxElement
  app_replace_answer: "Replace answer",
  // ui/Message.tsx:86 — JsxElement
  app_not_delivered: "Not delivered",
  // ui/Message.tsx:89 — JsxElement
  app_edit: "Edit",
  // ui/Message.tsx:117 — JsxElement
  app_previous_answer: "Previous answer",
  // ui/Message.tsx:137 — ConditionalExpression
  app_waiting_for_the_first_token: "Waiting for the first token…",
  // ui/Message.tsx:141 — JsxElement
  app_arriving_in_another_tab: "Arriving in another tab…",
  // ui/Message.tsx:146 — JsxElement
  app_your_message_was_carried_into_the_next_question: "Your message was carried into the next question.",
  // ui/Message.tsx:177 — JsxElement
  app_this_stopped_at_a_length_limit_on_the_host: "This stopped at a length limit on the host’s engine.",
  // ui/Message.tsx:188 — ConditionalExpression
  app_still_counted_against_today_s_tokens: " · still counted against today’s tokens",
  // ui/Message.tsx:191 — ConditionalExpression
  app_thought_despite_thinking_off: " · thought despite thinking off",
  // ui/Message.tsx:191 — ConditionalExpression
  app_thinking_off: " · thinking off",
  // ui/Message.tsx:196 — ConditionalExpression
  app_not_part_of_the_next_question: " · not part of the next question",
  // ui/Message.tsx:232 — ConditionalExpression
  app_copied: "Copied",
  // ui/Message.tsx:232 — ConditionalExpression
  app_copy: "Copy",
  // ui/Message.tsx:256 — ConditionalExpression
  app_hide: "hide",
  // ui/Markdown.tsx:22 — ConditionalExpression
  app_copied_lowercase: "copied",
  // ui/Markdown.tsx:22 — ConditionalExpression
  app_copy_lowercase: "copy",
  // ui/Markdown.tsx:34 — BinaryExpression
  app_image: "image",
  // api.ts:347 — NewExpression
  app_the_host_sent_a_reply_with_no_body: "the host sent a reply with no body",
  // api.ts:511 — PropertyAssignment
  app_the_host_could_not_read_that_request: "The host could not read that request",
  // api.ts:512 — PropertyAssignment
  app_this_is_the_app_s_fault_not_yours_start: "This is the app’s fault, not yours. Start a new chat; if it keeps happening the host and this app disagree about the API.",
  // api.ts:513 — PropertyAssignment
  app_the_host_has_no_such_endpoint: "The host has no such endpoint",
  // api.ts:515 — PropertyAssignment
  app_the_host_does_not_recognise_this_invite: "The host does not recognise this invite",
  // api.ts:516 — PropertyAssignment
  app_it_may_have_been_rotated_or_deleted_ask_host: "It may have been rotated or deleted. Ask {host} for a fresh code.",
  // api.ts:520 — PropertyAssignment
  app_your_invite_is_paused: "Your invite is paused",
  // api.ts:521 — PropertyAssignment
  app_ask_host_to_resume_it_then_try_again_your: "Ask {host} to resume it, then try again — your chats are still on this device.",
  // api.ts:522 — PropertyAssignment
  app_this_invite_was_revoked: "This invite was revoked",
  // api.ts:523 — PropertyAssignment
  app_ask_host_for_a_new_code: "Ask {host} for a new code.",
  // api.ts:525 — PropertyAssignment
  app_host_didn_t_answer: "{host} didn’t answer",
  // api.ts:526 — PropertyAssignment
  app_it_s_probably_asleep_or_offline_your_message_is: "It’s probably asleep or offline — your message is saved, try again in a minute. If the host upgraded recently, ask them for a fresh invite.",
  // api.ts:528 — PropertyAssignment
  app_host_stopped_answering_mid_reply: "{host} stopped answering mid-reply",
  // api.ts:529 — PropertyAssignment
  app_what_arrived_is_above_try_again_if_it_keeps: "What arrived is above. Try again — if it keeps happening, their machine may have gone to sleep.",
  // api.ts:530 — PropertyAssignment
  app_that_model_is_not_shared_with_you: "That model is not shared with you",
  // api.ts:531 — PropertyAssignment
  app_pick_one_of_the_models_in_the_picker_those: "Pick one of the models in the picker — those are the ones this invite may use.",
  // api.ts:532 — PropertyAssignment
  app_that_message_is_too_large_to_send: "That message is too large to send",
  // api.ts:533 — PropertyAssignment
  app_shorten_it_or_split_what_you_are_pasting_into: "Shorten it, or split what you are pasting into a couple of messages.",
  // api.ts:534 — PropertyAssignment
  app_this_conversation_no_longer_fits_the_model: "This conversation no longer fits the model",
  // api.ts:535 — PropertyAssignment
  app_start_a_new_chat_or_shorten_what_you_just: "Start a new chat, or shorten what you just sent.",
  // api.ts:536 — PropertyAssignment
  app_too_fast_for_this_invite: "Too fast for this invite",
  // api.ts:537 — PropertyAssignment
  app_host_allows_a_set_number_of_messages_a_minute: "{host} allows a set number of messages a minute. The count clears on its own.",
  // api.ts:538 — PropertyAssignment
  // Shared-invite concurrency cap; count is the host-provided limit.
  app_busy_invite_seats: "This invite is busy — all {count} seats are in use. Try again in a moment.",
  app_one_reply_at_a_time: "One reply at a time",
  // api.ts:539 — PropertyAssignment
  app_this_invite_may_have_one_request_in_flight_wait: "This invite may have one request in flight. Wait for the current reply to finish.",
  // api.ts:540 — PropertyAssignment
  app_today_s_token_budget_is_used_up: "Today's token budget is used up",
  // api.ts:541 — PropertyAssignment
  app_the_host_sets_a_daily_cap_per_invite_it: "The host sets a daily cap per invite. It resets, or they can raise it.",
  // api.ts:545 — PropertyAssignment
  app_host_is_busy: "{host} is busy",
  // api.ts:546 — PropertyAssignment
  app_every_slot_was_taken_your_message_is_still_here: "Every slot was taken — your message is still here.",
  // api.ts:547 — PropertyAssignment
  app_agent_unavailable: "The host's agent runtime is not available right now. Try again in a moment.",
  app_the_host_s_engine_is_offline: "The host's engine is offline",
  // api.ts:548 — PropertyAssignment
  app_their_machine_is_reachable_but_the_model_server_is: "Their machine is reachable but the model server is not running. Nothing you can fix.",
  // api.ts:549 — PropertyAssignment
  app_the_host_s_engine_returned_an_error: "The host's engine returned an error",
  // api.ts:550 — PropertyAssignment
  app_the_tunnel_and_the_gateway_are_fine_the_model: "The tunnel and the gateway are fine; the model server itself failed.",
  // api.ts:559 — BinaryExpression
  app_your_host: "your host",
  // api.ts:584 — BinaryExpression
  app_the_stream_ended_with_an_error: "The stream ended with an error.",
  // api.ts:587 — PropertyAssignment
  app_stopped: "Stopped",
  // api.ts:587 — PropertyAssignment; stream.ts:57
  app_you_stopped_this_reply: "You stopped this reply.",
  // api.ts:593 — PropertyAssignment
  app_the_tunnel_dropped_part_way_through_try_again_if: "The tunnel dropped part way through. Try again — if it keeps happening, their machine may have gone to sleep.",
  // stream.ts:52 — CallExpression
  app_the_connection_dropped_before_the_host_finished_this_reply: "The connection dropped before the host finished this reply — what is above is only part of it.",
  // stream.ts:85 — PropertyAssignment
  app_too_fast_not_sent: "Too fast — not sent.",
  // stream.ts:86 — PropertyAssignment
  app_not_sent_one_reply_at_a_time: "Not sent — one reply at a time.",
  // stream.ts:87 — PropertyAssignment
  app_not_sent_every_slot_was_taken: "Not sent — every slot was taken.",
  // stream.ts:88 — PropertyAssignment
  app_not_sent_today_s_tokens_are_used_up: "Not sent — today’s tokens are used up.",
  // stream.ts:89 — PropertyAssignment
  app_not_sent_your_invite_is_paused: "Not sent — your invite is paused.",
  // stream.ts:90 — PropertyAssignment
  app_not_sent_this_invite_was_revoked: "Not sent — this invite was revoked.",
  // stream.ts:91 — PropertyAssignment
  app_not_sent_this_invite_no_longer_works: "Not sent — this invite no longer works.",
  // stream.ts:112 — ConditionalExpression
  app_the_model_used_its_whole_reply_thinking_and_never: "The model used its whole reply thinking and never got to an answer. Regenerate, or ask for a shorter answer.",
  // stream.ts:113 — ConditionalExpression
  app_the_host_finished_without_sending_an_answer: "The host finished without sending an answer.",
  // stream.ts:120 — ConditionalExpression
  app_you_stopped_this_while_it_was_still_thinking: "You stopped this while it was still thinking.",
  // stream.ts:120 — ConditionalExpression
  app_you_stopped_this_before_it_began: "You stopped this before it began.",
  // stream.ts:273 — BinaryExpression
  app_measured_on_this_device_time_to_first_token_from: "Measured on this device. Time to first token: from Send to the first token, thinking or answer, with the relay hop and this app inside it.",
  // session.ts:309 — PropertyAssignment
  app_can_t_reach_the_relay_from_this_network: "Can’t reach the relay from this network",
  // session.ts:316 — PropertyAssignment
  app_it_s_probably_asleep_or_offline_ask_them_to: "It’s probably asleep or offline. Ask them to check that the host is running, then try again. If the host upgraded recently, ask them for a fresh invite.",
  // session.ts:330 — BinaryExpression
  app_the_host: "The host",
  // session.ts:412 — BinaryExpression
  app_your_host_variant: "Your host",
  // storage.ts:237 — PropertyAssignment
  app_untitled_chat: "Untitled chat",
  // storage.ts:382 — PropertyAssignment
  app_this_reply_was_still_arriving_when_its_tab_was: "This reply was still arriving when its tab was closed or reloaded — what is above is only part of it.",
  // invite.ts:42 — VariableDeclaration
  app_this_invite_needs_a_newer_version_of_the_app: "This invite needs a newer version of the app.",
  // invite.ts:51 — NewExpression
  app_paste_the_invite_code_your_host_sent_you: "Paste the invite code your host sent you.",
  // invite.ts:73 — NewExpression
  app_this_invite_is_missing_a_part_it_looks_cut: "This invite is missing a part — it looks cut off.",
  // invite.ts:76 — NewExpression
  app_the_address_in_the_middle_of_this_invite_is: "The address in the middle of this invite is not a host address.",
  // invite.ts:81 — NewExpression
  app_the_secret_at_the_end_of_this_invite_contains: "The secret at the end of this invite contains characters that do not belong in it.",
  // App.tsx:191 — fallback
  app_opening: "Opening…",
  // Chat: accessible name for delete; title is the user-authored chat title.
  app_delete_chat: "Delete {title}",
  // Chat: one omitted earlier turn; context is already compacted, host is a name/fallback.
  app_earlier_message_omitted: "One earlier message was too long for the {context} memory on {host} and was left out of this question.",
  // Chat: plural omitted turns.
  app_earlier_messages_omitted: "{count} earlier messages were too long for the {context} memory on {host} and were left out of this question.",
  // Chat: disabled retry countdown; duration keeps machine units.
  app_try_again_in: "Try again in {duration}",
  // Fallback host name, initial capital.
  app_this_host: "This host",
  // Fallback host name inside a sentence.
  app_the_host_lowercase: "the host",
  // Limits sheet: first-token median; preformatted duration.
  app_first_token_time: "{duration} to the first token",
  // Limits sheet: completion rate and per-token duration; leading separator preserved.
  app_per_token_speed: " · {rate}, {duration} per token",
  // Limits sheet: exactly one measured reply.
  app_median_one_reply: "The median over the one reply in this chat, measured on this device: Send to the first token, thinking or answer; completion tokens over first-to-last token, thinking included.",
  // Limits sheet: plural measured replies.
  app_median_replies: "The median over the {count} replies in this chat, measured on this device: Send to the first token, thinking or answer; completion tokens over first-to-last token, thinking included.",
  // Limits sheet: both metrics include relay transit; rtt is rounded milliseconds.
  app_relay_hop_in_timing: " The relay hop is inside both — the {rtt} ms round trip in the header right now.",
  // Empty chat headline; only shown for a named host.
  app_you_are_on_host: "You’re on {host}.",
  // Empty chat; model id/label is unchanged.
  app_model_unavailable: "{model} is not answering right now.",
  // Empty chat fallback when the model is unnamed.
  app_the_model: "The model",
  // Empty chat; model id/label is unchanged.
  app_model_listening: "{model} is listening.",
  // Settings: value uses existing formatting.
  app_temperature: "Temperature · {value}",
  // Settings: hint when thinking control has not been observed.
  app_thinking_model_default: "· model default",
  // Settings: key name and id are machine strings; preserve inline code styling at wiring.
  app_settings_invite: "Your invite is {name} ({id}).",
  // Settings: four numeric limits; daily/output already compacted.
  app_settings_limits: "Limits: about {rpm} messages a minute, {daily} tokens a day, {concurrent} at a time, and up to {output} tokens in any one reply.",
  // Settings: upstream kind is unchanged.
  app_settings_engine: "Engine: {engine}",
  // Settings: optional engine context after engine kind.
  app_settings_context: ", {context} context",
  // Settings: selected raw model id is unchanged; preserve inline code styling.
  app_settings_model_id: "Model id: {model}.",
  // Settings: degraded path; ago is the localized relative-time phrase.
  app_path_last_measured: " The path last measured {ago}.",
  // Limits sheet bold introductory clause.
  app_limits_rpm: "About {rpm} messages a minute.",
  // Limits sheet bold introductory clause; compacted count.
  app_limits_daily: "{daily} tokens a day.",
  // Limits sheet bold introductory clause; compacted count.
  app_limits_context: "{context} tokens of context.",
  // Connect progress, host unnamed.
  app_connecting: "Connecting…",
  // Connect progress, named host.
  app_connecting_host: "Connecting to {host}…",
  // Connect slow progress, host unnamed.
  app_still_connecting: "Still connecting…",
  // Connect slow progress, named host.
  app_still_connecting_host: "Still connecting to {host}…",
  // Returning/kept card: count=1; welcome heading remains separately bold.
  app_kept_chat: "Your {count} chat with {host} is still on this device.",
  // Returning/kept card: count!=1.
  app_kept_chats: "Your {count} chats with {host} are still on this device.",
  // Developer-only checkbox label; URL unchanged.
  app_direct_mode_dev: "Direct mode (dev) — talk to {url} instead of the tunnel",
  // Idle invalid-format hint, distinct from detailed parser errors.
  app_invalid_invite_hint: "That doesn’t look like an {product} invite yet.",
  // Log-prompts disclosure title; named host or This host.
  app_host_records_input: "{host} is recording what you write",
  // Log-prompts disclosure: key name/id stay machine strings and code styled.
  app_connected_identity: "Connected as {name} ({id}). Nothing has been sent yet.",
  // Message queue state; host fallback the host.
  app_waiting_slot: "Waiting for a free slot on {host}…",
  // Message before first token, host slow.
  app_still_waiting_host: "Still waiting for {host}…",
  // Message ending caused by per-key output limit.
  app_reply_limit: "This stopped at your invite’s {limit}-token reply limit.",
  // Message ending caused by model context; context compacted.
  app_context_filled: "This chat has filled the {context} memory on {host}, so this reply stopped short. Replies will keep getting shorter until a message no longer fits — start a new chat for a clean slate.",
  // Message metadata: input/output usage; preserve separators.
  app_message_usage: " · {input} tokens in · {output} out",
  // Message metadata after regeneration with thinking disabled.
  app_tokens_saved: " · {count} fewer tokens than the previous reply",
  // Collapsed thinking disclosure; current English uses words even for 1.
  app_thinking_words: "{count} words",
  // API fallback when HTTP error has no usable response message; status unchanged.
  app_host_http_answer: "The host answered {status}.",
  // API not_found detail; product name unchanged.
  app_wrong_gateway: "This app is talking to something that is not an {product} gateway, or to an older one.",
  // API unknown error title; status unchanged.
  app_host_answered_status: "{host} answered {status}",
  // API streamed unknown error title.
  app_host_stopped_reply: "{host} stopped the reply",
  // API transport failure title.
  app_connection_broke: "The connection to {host} broke",
  // Stream stalled before answer and before any thinking; title is already localized.
  app_stalled_before_answer: "{title} — nothing of the answer had arrived yet. Try again — if it keeps happening, their machine may have gone to sleep.",
  // Stream stalled before answer but after thinking; title is already localized.
  app_stalled_after_thinking: "{title} — nothing of the answer had arrived yet, only its thinking. Try again — if it keeps happening, their machine may have gone to sleep.",
  // Stream combines already-localized error title/detail; punctuation only.
  app_error_title_detail: "{title}. {detail}",
  // Speed metadata: a lower bound on measured queue time.
  app_queued_time: " (≥{duration} of it in line for a slot)",
  // Speed tooltip: duration preformatted; 5 s is the protocol keepalive interval.
  app_queue_timing_explanation: " The host said this request was in line for a slot for at least {duration} of that; it says so every 5 s and nothing when the slot comes, so the rest is not all the model's.",
  // Speed tooltip; rate/duration units unchanged.
  app_token_speed_explanation: " {rate} is {duration} per token: completion tokens over first-to-last token, thinking included.",
  // Handshake error; directory and region are machine strings.
  app_relay_directory_unreachable: "The relay directory at {directory} (region {region}) did not answer, so nothing left this device — the invite itself is fine. Check the connection, or try another network.",
  // Path chip without a prior measurement; healthy direct/relayed paths stay machine strings.
  app_path_not_answering: "{host} — not answering",
  // Path chip showing old measurement; rtt unchanged, ago localized.
  app_path_not_answering_last: "{host} — not answering · last {rtt} ms {ago}",
  // RPM meter when usage is unknown; limit unchanged.
  app_meter_minute_unknown: "— / {limit} per minute",
  // RPM meter with exactly one remaining message.
  app_meter_message_left: "{count} message left this minute",
  // RPM meter with any other remaining count.
  app_meter_messages_left: "{count} messages left this minute",
  // RPM meter when the host set no per-minute cap.
  app_meter_messages_used: "{count} messages this minute",
  // Daily meter when usage is unknown; limit compacted.
  app_meter_daily_unknown: "— / {limit} tokens today",
  // Daily meter with a cap; both values compacted.
  app_meter_daily: "{used}/{limit} tokens today",
  // Daily meter without a cap.
  app_meter_daily_uncapped: "{used} tokens today",
  // Context meter before a known usage count.
  app_meter_context_unknown: "— / {limit} context",
  // Context meter; compacted counts.
  app_meter_context: "{used}/{limit} context",
  // Degraded header: paused key.
  app_invite_paused_banner: "{host} paused your invite. Your message is still here — try again once they resume.",
  // Degraded header: revoked key.
  app_invite_revoked_banner: "This invite was revoked — ask {host} for a new code.",
  // Degraded header: invalid/deleted key.
  app_invite_invalid_banner: "{host} no longer recognises this invite — ask them for a new code.",
  // Degraded header: upstream kind unchanged.
  app_engine_offline_banner: "{engine} is not answering on the host — messages will fail until it is back",
  // Relative time: seconds; retain machine unit s.
  app_ago_seconds: "{count} s ago",
  // Relative time: minutes; retain machine unit min.
  app_ago_minutes: "{count} min ago",
  // Relative time: hours; retain machine unit h.
  app_ago_hours: "{count} h ago",
  // Parser: prefix and clipped found tag unchanged.
  app_invite_bad_prefix: "An invite starts with \"{prefix}.\" — this one starts with \"{found}\".",
  // Parser: prefix and format example unchanged.
  app_invite_part_count: "An invite has three dot-separated parts ({prefix}.address.secret); this one has {count}.",
  // Dynamic privacy line used in Chat/Connect; computer phrase localized, name unchanged.
  app_privacy_normal: "Encrypted end-to-end from your device to {computer} — the relay in between can’t read it. {product} request logs record counts, never text. The model runs on their machine. Chat history stays on your device. The host stores job and run inputs, outputs and trajectories under your key until they expire.",
  // Dynamic logging privacy line; computer phrase localized.
  app_privacy_logging: "Encrypted end-to-end from your device to {computer} — but this host has prompt logging on, so everything you send and everything the model answers is written to a log on their machine. Chat history stays on your device. The host stores job and run inputs, outputs and trajectories under your key until they expire.",
  // Computer-name formatter: empty host name.
  app_host_computer_unnamed: "your host’s computer",
  // Computer-name formatter: name already describes a device or possessive.
  app_host_computer_named: "the computer named {name}",
  // Computer-name formatter: a person-like host name.
  app_host_computer_person: "{name}’s computer",
  // Chat.tsx: sidebar toggle accessible name.
  app_conversations: "Conversations",
  // Limits-sheet close and output-limit continuation, PM-approved additions.
  app_got_it: 'Got it',
  app_continue: 'Continue',
  app_file_remove: "Remove file",
  app_file_chip_line: "{kind} · {extent}{tokens} tokens",
  app_file_reading: "{kind} · reading…",
  app_file_single_line: "1 file · {tokens} tokens",
  app_files_line: "{count} files · {tokens} tokens",
  app_file_page: "1 page",
  app_file_pages: "{count} pages",
  app_file_line: "1 line",
  app_file_lines: "{count} lines",
  app_file_no_text: "Couldn’t find any text in {name} — a scanned page or a picture has none. Attach it as an image instead.",
  app_file_no_text_no_vision: "Couldn’t find any text in {name} — a scanned page or a picture has none, and {model} can’t see images.",
  app_file_cant_read: "Can’t read {name} — PDF, Word, text and code only.",
  app_file_over_context: "Over the {context} tokens {model} holds — remove a file.",
  app_file_pasted_text: "Pasted text",
  app_file_sent_caption: "{measure} · as sent to {host}",
  app_file_n_of: "File {n} of {count}: {name}",
  app_file_count: "Up to 4 files per message — remove a file.",
  app_file_storage: "This chat holds 512 KiB of file text — start a new chat or remove a file.",
  app_install_ask: "Keep Infercat on your Home Screen?",
  app_install_add: "Add",
  app_install_not_now: "Not now",
  app_install_settings_phone: "Add Infercat to your Home Screen — one tap to this chat.",
  app_install_settings_action_phone: "Add to Home Screen",
  app_install_settings_desktop: "Install Infercat as an app — it opens in its own window.",
  app_install_settings_action_desktop: "Install",
  app_install_settings_safari_desktop: "Safari puts Infercat in your Dock: File → Add to Dock.",
  app_install_installed_phone: "Installed — opens from your Home Screen.",
  app_install_installed_desktop: "Installed as an app — this is its window.",
  app_ios_sheet_title: "Add to Home Screen",
  app_ios_sheet_lead: "Safari adds it by hand — three steps.",
  app_ios_step_1: "Copy your invite. The app on your Home Screen starts empty, so you will paste it once more.",
  app_ios_copy_invite: "Copy invite",
  app_ios_step_2: "In Safari 26, tap **…** below the page, then **Share** ⎙ → **View More** → **Add to Home Screen**.",
  app_ios_step_2_older: "On older Safari, Add to Home Screen is in the Share sheet directly.",
  app_ios_step_3: "Open Infercat from your Home Screen and tap **Paste**.",
  app_path_no_network: "No network on this device",
  app_offline_hint: "Your chats are readable. Sending needs a network.",
  app_offline_card: "No network on this device. The card will connect when it is back.",
  app_path_reconnecting: "{host} — reconnecting…",
  app_this_chat_is_open_in_another_window: "This chat is open in another window.",
  app_use_this_window_instead: "Use this window instead",
} as const;
