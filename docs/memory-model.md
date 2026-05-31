# Memory Model

The primary unit is a trace. Traces represent durable memory items such as facts, observations, decisions, preferences, procedures, events, warnings and questions.

## Trace Kinds

- `fact`
- `observation`
- `decision`
- `preference`
- `procedure`
- `event`
- `episode`
- `semantic_summary`
- `warning`
- `question`

## Statuses

- `active`
- `inhibited`
- `consolidated`
- `archived`
- `forgotten`

## Entities

Entities represent concepts, people, files, projects, decisions and systems. They connect traces into a semantic graph that can be used during associative retrieval.

## Associations

Associations are weighted edges between traces. They record relation, strength, confidence, evidence count and last reinforcement time.

## Hygiene

Agents should save durable facts, decisions, useful observations, procedures and preferences. They should avoid storing raw transcript dumps unless the transcript itself is the durable artifact.

