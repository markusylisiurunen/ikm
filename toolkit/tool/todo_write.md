Use this tool to create and manage a structured task list for your current session. This helps you track progress, organize complex tasks, and demonstrate thoroughness to the user. It also helps the user understand the progress of the task and overall progress of their requests.

## When to use this tool

Use this tool proactively in these scenarios:

1. Complex multi-step tasks - When a task requires 3 or more distinct steps or actions.
2. Non-trivial and complex tasks - Tasks that require careful planning or multiple operations.
3. User explicitly requests todo list - When the user directly asks you to use the todo list.
4. User provides multiple tasks - When users provide a list of things to be done.
5. After receiving new instructions - Immediately capture user requirements as todos. Feel free to edit the todo list based on new information.
6. After completing a task - Mark it complete and add any new follow-up tasks if necessary.
7. When you start working on a new task, mark the todo as `in_progress`. Ideally you should only have one todo as `in_progress` at a time. Complete existing tasks before starting new ones.

## When NOT to use this tool

Skip using this tool when:

- There is only a single, straightforward task.
- User explicitly asks you to not use a todo list but rather start working immediately.
- The task is trivial and tracking it provides no organizational benefit.
- The task can be completed in less than 3 trivial steps.
- The task is purely conversational or informational.

**NOTE:** You should not use this tool if there is only one trivial task to do. In this case you are better off just doing the task directly.

# Task states and management

1. **Task states:** Use these states to track progress:
   1. `pending`: Task not yet started
   2. `in_progress`: Currently working on (limit to ONE task at a time)
   3. `completed`: Task finished successfully
   4. `cancelled`: Task no longer needed
2. **Task management:**
   1. Update task status in real-time as you work
   2. Mark tasks complete IMMEDIATELY after finishing (don't batch completions)
   3. Only have ONE task `in_progress` at any time
   4. Complete current tasks before starting new ones
   5. Cancel tasks that become irrelevant
3. **Task breakdown:**
   - Create specific, actionable items
   - Break complex tasks into smaller, manageable steps
   - Use clear, descriptive task names

When in doubt, use this tool. Being proactive with task management demonstrates attentiveness and ensures you complete all requirements successfully.

## Example scenarios for using the todo list

<example_1>
<message_history>
User: I want to add a dark mode toggle to the application settings. Make sure you run the tests and build when you're done!
Assistant: I'll help add a dark mode toggle to your application settings. Let me create a todo list to track this implementation.

Creates todo list with the following items:

1. Create dark mode toggle component in Settings page
2. Add dark mode state management (context/store)
3. Implement CSS-in-JS styles for dark theme
4. Update existing components to support theme switching
5. Run tests and build process, addressing any failures or errors that occur
6. Begins working on the first task

</message_history>
<reasoning>
The assistant used the todo list because:

- Adding dark mode is a multi-step feature requiring UI, state management, and styling changes
- The user explicitly requested tests and build be run afterward
- The assistant inferred that tests and build need to pass by adding "Ensure tests and build succeed" as the final task

</reasoning>
</example_1>

<example_2>
<message_history>
User: I need to plan a comprehensive marketing campaign for our product launch. This should include social media strategy, email marketing, influencer outreach, and budget allocation.
Assistant: I'll help you plan a comprehensive marketing campaign for your product launch. Let me create a todo list to organize all the campaign components.

Creates todo list with the following items:

1. Develop social media content calendar and posting schedule
2. Design email marketing sequence with welcome, announcement, and follow-up messages
3. Research and create influencer outreach list with contact templates
4. Create budget allocation spreadsheet with cost breakdowns for each channel
5. Establish campaign timeline and milestone tracking system

</message_history>
<reasoning>
The assistant used the todo list because:

1. The user provided multiple complex marketing activities that need coordination
2. Each component (social media, email, influencers, budget) requires multiple sub-tasks
3. A marketing campaign benefits from systematic planning and tracking
4. This approach ensures all campaign elements are properly coordinated and executed

</reasoning>
</example_2>

<example_3>
<message_history>
User: Can you help me write a professional email to my manager requesting a meeting to discuss my career development? I want to make sure it's polite but clear about my intentions. A draft is in `draft.txt`.
Assistant: I'll help you craft a professional email to your manager about career development. I'll edit the draft in `draft.txt` and ensure it meets your requirements.

Assistant directly reads and edits the draft in `draft.txt`.
</message_history>
<reasoning>
The assistant did NOT use the todo list because:

- This is a single, straightforward task (refining one email)
- The request can be completed immediately without complex planning
- No multiple steps or coordination between different activities is required
- The task is completed in one action rather than requiring progress tracking
- Adding a todo list would create unnecessary overhead for what is essentially a writing task

</reasoning>
</example_3>
