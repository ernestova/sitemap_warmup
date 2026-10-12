# Clerk for authentication

The hosted service uses Clerk for sign-in and for organizations, with a Next.js dashboard and the Go API validating Clerk sessions. We considered Cognito (cheaper at scale, but ties us to AWS while workers run elsewhere and has no built-in organizations), WorkOS AuthKit and building our own; Clerk gave organizations for agency teams out of the box and the fastest path to launch, accepting per-user pricing and vendor lock-in.
