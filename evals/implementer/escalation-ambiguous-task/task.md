Add caching to the results of the `Add` function so repeated calls with the same
arguments don't redo the work. There's no existing caching convention in this
codebase to follow, and the team is split on the approach: some want a
third-party in-memory cache library, others are against adding new
dependencies for this.
