# INVITE and LISTEN placeholders are filled from this recording's actual key and free port.
Source style.tape
Output friend.mp4
Output friend.ascii
Hide
Type@1ms `export PS1='$ '; trap '' HUP; clear`
Enter
Sleep 500ms
Show
Type `infercat connect `
Type@1ms `@INVITE@`
Type ` --listen 127.0.0.1:@LISTEN@ &`
Enter
Hide
Type@1ms `echo $! > friend.pid`
Enter
Show
Wait+Screen@60s /path.*(direct|relayed)/
Sleep 2s
Type `curl -fsS 127.0.0.1:@LISTEN@/v1/chat/completions --json @question.json | jq -er '.choices[0].message.content'`
Enter
Wait+Line@60s /^\$\s*$/
Sleep 2.5s
Hide
Type@1ms `echo $? > response.status`
Enter
Sleep 200ms
