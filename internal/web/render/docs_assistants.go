package render

func docsAssistantsTopic() DocsTopic {
	return DocsTopic{
		Key:   "assistants",
		Title: "Assistants",
		Icon:  "ti-sparkles",
		Lead:  "Your own conversation partners, as many as you want, on every device.",
		Items: []DocsItem{
			{
				Title: "One place, always open",
				Desc:  `The sparkle <i class="ti ti-sparkles align-text-bottom" aria-hidden="true"></i> in the rail, the tab bar and the terminal switcher opens the assistant you looked at last, the list beside the thread, its watching aside behind the eye <i class="ti ti-eye align-text-bottom" aria-hidden="true"></i>, the memory behind the brain in the list's head. A message's left stripe says who spoke: blue you, purple an answer, grey what the cockpit brings unasked.`,
			},
			{
				Title:    "It sees the cockpit",
				Tag:      "Live state",
				TagClass: "bg-blue-lt",
				Desc:     `Ask what is running, what is waiting for you, or what happened while you were away: it reads the coders, the shells, the projects and the unread notifications. It types into a coder too, a prompt or the keys a dialog needs; a shell it can only read.`,
			},
			{
				Title:    "It remembers you",
				Tag:      "Memory",
				TagClass: "bg-blue-lt",
				Desc:     `Say remember that and it writes it down. The brain <i class="ti ti-brain align-text-bottom" aria-hidden="true"></i> in the list's head opens what it knows, to read and to correct. The memory is shared, so what you tell one they all know.`,
			},
			{
				Title:    "Pictures, recordings and clips",
				Tag:      "Files",
				TagClass: "bg-blue-lt",
				Desc:     `Drop files onto the assistant or paste them into the message box; the paperclip <i class="ti ti-paperclip align-text-bottom" aria-hidden="true"></i> does the same.`,
			},
			{
				Title:    "Point at a file",
				Tag:      "Mention",
				TagClass: "bg-blue-lt",
				Desc:     `<kbd>@</kbd> at the start of a word in the message box lists the projects, worktrees included, and a picked project its files, both narrowed as you type like the editor's palettes. The arrows move, <kbd>Enter</kbd> or <kbd>Tab</kbd> picks, <kbd>Esc</kbd> closes, and <kbd>Backspace</kbd> right after the project or the arrow <i class="ti ti-chevron-left align-text-bottom" aria-hidden="true"></i> goes back to the projects; on a phone tap a row. A file lands as its absolute path, the first row before you type is the project's folder. An absolute path of a project's file links to that file in its project's editor, in your messages and in the answers, code included, and <code>:line</code>, <code>:line:column</code> or <code>:line-line</code> after it places the cursor; <kbd>Ctrl</kbd>, <kbd>Cmd</kbd> or <kbd>Shift</kbd> opens a new tab. A relative path, a bare name and a quoted path with a blank stay text, and an answer links once it is complete. The id of a running coder the assistant steers or steered, in full or its first eight characters, links to that coder.`,
			},
			{
				Title:    "Talk to it",
				Tag:      "Voice",
				TagClass: "bg-blue-lt",
				Desc:     `With an empty message box the button beside it is the microphone <i class="ti ti-microphone align-text-bottom" aria-hidden="true"></i>: hold it and it records, release and what you said is transcribed and sent. Slide left while holding to cancel, slide up to keep recording without holding, then the trash <i class="ti ti-trash align-text-bottom" aria-hidden="true"></i> discards and the send <i class="ti ti-send align-text-bottom" aria-hidden="true"></i> sends. A click keeps recording without holding too, <kbd>Alt</kbd> twice does the same from anywhere, <kbd>Esc</kbd> discards, and with anything typed or attached the button is the plain send. German and English both work without a language setting. The first recording waits once while the cockpit builds the container.`,
			},
			{
				Title:    "It talks back",
				Tag:      "Voice",
				TagClass: "bg-blue-lt",
				Desc:     `The speaker <i class="ti ti-volume align-text-bottom" aria-hidden="true"></i> on an answer or a report reads it aloud, code blocks left out, in the language it is written in. Voice mode <i class="ti ti-volume align-text-bottom" aria-hidden="true"></i> in the assistant's head reads every finished answer on its own, per device. Settings &rsaquo; Assistants &rsaquo; Voice picks how each engine runs.`,
			},
			{
				Title:    "As many as you want",
				Tag:      "Side by side",
				TagClass: "bg-blue-lt",
				Desc:     `The new-assistant button <i class="ti ti-message-plus align-text-bottom" aria-hidden="true"></i> starts another one, asking which coder answers when more than one is installed. Each keeps its own thread and its own steered coders and they all stay live. Its row says the name, how many coders it holds and when it last spoke; its menu renames, shares and deletes it, thread and files included.`,
			},
			{
				Title:    "Say the next thing right away",
				Tag:      "Queue",
				TagClass: "bg-blue-lt",
				Desc:     `A message sent while an answer is on its way waits instead of being refused: it stands in the thread marked Waiting, Don't send takes it back, and everything waiting goes out as one when the answer arrives. The queue is that assistant's alone.`,
			},
			{
				Title:    "Answer its questions",
				Tag:      "Ask",
				TagClass: "bg-blue-lt",
				Desc:     `When the assistant needs a decision, its answer ends in an <code>ask</code> block, shown as questions with their options as buttons and a field under each for a note or an answer in your own words. Tap an option, tap it again to take it back. A question the assistant wrote with <code>- [ ]</code> options takes several picks, each option toggles on its own. What you picked and wrote is kept with the assistant until you send, so it stands the same after a reload and on your other devices. <em>Send answers</em> stays off until every question has an option or words, then sends the assistant each question with what you picked and wrote. <kbd>Ctrl</kbd>+<kbd>Enter</kbd> in a field sends too. Your message for it is one slim row that says you answered, the block keeps what you picked. Once sent, or once you wrote something else instead, the questions take nothing more. While the answer streams, and where a block is malformed, they show as plain text. A shared link shows them read only.`,
			},
			{
				Title:    "Put the list in your own order",
				Tag:      "Sorting",
				TagClass: "bg-blue-lt",
				Desc:     `Drag a row where you want it, on a phone by its grip <i class="ti ti-grip-vertical align-text-bottom" aria-hidden="true"></i>. The order is kept on the server, so it comes back after a reload and on every other device, and an answer moves nobody.`,
			},
			{
				Title: "Step through the assistants",
				Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Tab"}}, {Caps: []string{"Ctrl", "Shift", "Tab"}}},
				Desc:  `Steps through the list in the order it stands in and wraps at either end.`,
			},
			{
				Title:    "Swipe to the next assistant",
				Tag:      "Touch",
				TagClass: "bg-blue-lt",
				Desc:     `On a phone swipe left or right on the conversation to go to the next or the previous assistant in the list, wrapping at both ends, and a pill names the one you would land on. Up and down stays scrolling. The swipe steps aside while text is selected, after a long press, in the message box and on a code block that scrolls sideways.`,
			},
			{
				Title:    "How full one is",
				Tag:      "Context",
				TagClass: "bg-blue-lt",
				Desc:     `A ring around the button beside the composer fills with how much of the coder's context window this assistant takes up, orange from 85 percent, red from 95. Its icon is the coder this assistant runs on. It moves once per answer and stays empty for a model whose window this cockpit does not know. Its menu picks this assistant's Chat, Checks and Triggers models. Empty Chat runs on the coder's start default, empty Checks and Triggers on the chat.`,
			},
			{
				Title:    "What it may do",
				Tag:      "Tools",
				TagClass: "bg-blue-lt",
				Desc:     `It has the tools of a coder: reading and searching, writing files, running commands, fetching a page from the web. It belongs to no project, and its files stay in a workspace of its own. A finished answer notifies like any other news and names which assistant answered.`,
			},
			{
				Title:    "It hands work over and steers it",
				Tag:      "Jobs",
				TagClass: "bg-blue-lt",
				Desc:     `Changes in your projects are not its job: it starts a coder for the task, briefs it, and steers it from the start. It also resumes, stops and deletes coders, and creates and deletes projects. A job is one coder, one task and one criterion that decides the task is done, and you can steer any coder yourself. A steered coder that finishes, asks something or stops moving buys a check; only done and blocked reach you, at ten checks and eight hours per job. Exactly one assistant steers a coder. A check the coder refuses to start, not logged in or an unknown model, closes the job as blocked and says why; any other failed check gets one more try.`,
			},
			{
				Title:    "Watching: the coders and the triggers",
				Tag:      "Aside",
				TagClass: "bg-blue-lt",
				Desc:     `What an assistant carries on by itself stands beside its thread on a wide window, below that behind the eye <i class="ti ti-eye align-text-bottom" aria-hidden="true"></i>, in two tabs: the coders it steers and the triggers it waits for, the badge counting both. A row says who it is, and its icon says where it stands: purple while it is alive, purple with a dot running along its edge while the assistant works on it, red where it ended without arriving, grey where it is over. Its actions stand under it open or shut, and its fold holds the text: what the coder was sent, what it is measured against, the last report and until when it runs.`,
			},
			{
				Title:    "A note: the cockpit speaks",
				Tag:      "Notes",
				TagClass: "bg-blue-lt",
				Desc:     `A grey message is one nobody asked for, a check's report or the answer a trigger pushed. Both read the same way: the headline, under it the result and nothing else, standing as it is when it is short and behind the first words, as many as a notification on a phone carries, with the rest on a tap when it is longer. While an answer streams they wait in a bar above the composer and land under the finished answer without moving the page. Your next message tells the assistant how many arrived since its last answer.`,
			},
			{
				Title:    "It reacts to events",
				Tag:      "Triggers",
				TagClass: "bg-blue-lt",
				Desc:     `A trigger hangs the assistant onto an event: a job closing done, blocked or expired, a coder ending its turn or asking, a cron schedule. Job closed covers all three job ends and Coder signals both coder signals. The reaction runs on its own, never in the conversation, and its answer is pushed into the thread marked <i class="ti ti-bolt align-text-bottom" aria-hidden="true"></i> and notified, unless its first line is NOTHING. New trigger <i class="ti ti-plus align-text-bottom" aria-hidden="true"></i> over the Triggers tab makes one, with an optional name the row and the notification then read by; Open coder, Change and Remove stand under every row, and Change moves everything but the event. A trigger may run on a model of its own, picked in its form; empty means the assistant's Triggers pick, else its chat model. A trigger stands until you remove it unless you give it an expiry, any number of minutes, hours or days, or make it a one shot, and one that names several terminals fires on any of them or waits for all of them. A schedule carries the time zone its wall clock times are read in, which stands beside its crontab fields on the row and is what the next tick is shown in; picking another one in the form is also what the next schedule then starts on. Around a daylight saving changeover a schedule follows the wall clock: a time the spring change skips falls out that day, an hour the autumn change repeats fires twice.`,
			},
			{
				Title:    "Worktrees",
				Tag:      "Git",
				TagClass: "bg-blue-lt",
				Desc:     `An assistant lists a project's worktrees and makes new ones, named <code>&lt;project&gt;-&lt;branch&gt;</code>. The worktree post script runs in it, the assistant reads its output. It reads the post script and replaces it once you approve.`,
			},
			{
				Title:    "Compose commands",
				Tag:      "Docker",
				TagClass: "bg-blue-lt",
				Desc:     `An assistant runs a project's compose commands in the background, and each end lands as a grey note in its thread that rings you: done or failed, with exit code, output tail and a link to the run, and after a start or a restart that went through, the addresses the stack answers on as links. It picks the command by its intent, the icon it carries in Settings &rsaquo; Docker, and asks you when that leaves a choice. A command marked to ask first starts only once you approve it, see Approvals. Your own Cancel rings nobody.`,
			},
			{
				Title:    "Approvals",
				Tag:      "Approval",
				TagClass: "bg-blue-lt",
				Desc:     `A compose command marked to ask first an assistant's delete of a project, a coder or an assistant and its write of the worktree post script wait for your <em>Approve</em> in a dialog on every page, which names the action, the assistant asking and what it acts on: the stack and the project with the command line and its directory, the project, the coder and its project, the assistant, or the new script, an assistant or a coder with its short id behind the name. An assistant deleting several of one kind asks once for all of them, and the note names what went and what failed and why. Nothing runs before it. <em>Approve</em> runs it as things stand then, and anything gone meanwhile fails it; <em>Deny</em>, half an hour without an answer or a restart decline it. Either way a grey note lands in the assistant's thread, and it rings you only when nobody decided or the approved action failed. <em>Approve and don't ask again</em>, or its switch under Settings &rsaquo; Assistants &rsaquo; Approvals, turns that question off for every assistant.`,
			},
			{
				Title:    "Steer and release where the coder is",
				Tag:      "Ownership",
				TagClass: "bg-blue-lt",
				Desc:     `A steered coder's icon turns purple wherever it shows and follows steer and release without a reload. Both sit where the coder is listed: its tab's context menu (on a phone behind the three dots of its row), its pane header, its chip. The dialog asks which assistant the reports go to once there is more than one.`,
			},
		},
	}
}
