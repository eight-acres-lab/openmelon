# Why OpenMelon

Content production needs more than one-shot prompting. A real workflow keeps state across many turns — which project the work belongs to, which character must look the same across posts, which generations are drafts versus shippable. A chat window doesn't keep that.

OpenMelon is a terminal agent for content production. Each project is a directory with persistent character and reference libraries; the agent uses tools to pull from them while drafting, anchors images to the same portraits across runs, and logs every turn so sessions are inspectable and resumable.

It runs locally against any LLM and image model you have an API key for (OpenRouter, OpenAI, Anthropic). Skills — the prompt + output-schema bundles that turn intent into a generation prompt — live in [skillplus](https://github.com/eight-acres-lab/skillplus).
