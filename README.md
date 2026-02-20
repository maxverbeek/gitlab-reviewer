# gitlab-reviewer

A small CLI tool that lists GitLab project members for the current repository.
It outputs names and usernames in TSV format, making it easy to pipe into `fzf`
or other selectors to pick a merge request reviewer.

## How it works

1. Detects the GitLab project from the `origin` remote (SSH or HTTPS).
2. Fetches project members from the GitLab API using a personal access token.
3. Caches results for 24 hours (in `~/.cache/gitlab-reviewer/`).
4. Falls back to stale cache, then `git log` contributors if the API is unavailable.

## Setup

### GitLab personal access token

Create a file `~/.gitlab_pat` containing a
[GitLab personal access token](https://docs.gitlab.com/ee/user/profile/personal_access_tokens.html)
with `read_api` scope:

```sh
echo "glpat-xxxxxxxxxxxxxxxxxxxx" > ~/.gitlab_pat
chmod 600 ~/.gitlab_pat
```

### Install with Nix

Add the flake as an input and include the package in your environment:

```nix
# flake.nix
{
  inputs.gitlab-reviewer.url = "github:maxverbeek/gitlab-reviewer";

  # ...

  # In your system/home-manager config:
  environment.systemPackages = [
    inputs.gitlab-reviewer.packages.${system}.default
  ];
}
```

Or run it directly without installing:

```sh
nix run github:maxverbeek/gitlab-reviewer
```

## Usage

```sh
# TSV output (name<TAB>username)
gitlab-reviewer

# JSON output
gitlab-reviewer -json

# Force refresh the cache
gitlab-reviewer -refresh
```

## Integration

### Shell (fzf)

Use a git alias to push and create a merge request with a reviewer selected via
`fzf`:

```gitconfig
[alias]
    mpr  = "!f() { git push -o merge_request.create -o \"merge_request.description=/assign_reviewer @$1\" 2>&1 | xtee -p \"https://\\\\S+\" -e wl-copy -e xdg-open >&2; }; f"
    mprf = "!f() { reviewer=$(gitlab-reviewer | fzf --with-nth=1 --delimiter=$'\\t' | cut -f2) || return; [ -n \"$reviewer\" ] && git mpr \"$reviewer\"; }; f"
```

`git mprf` lets you fuzzy-search project members by name, then pushes and
creates a merge request with the selected person as reviewer.

### Neovim

Add a command that calls `gitlab-reviewer`, presents a `vim.ui.select` picker
(enhanced by [dressing.nvim](https://github.com/stevearc/dressing.nvim) or
similar), and pushes with the chosen reviewer via the `mpr` alias above:

```lua
vim.api.nvim_create_user_command("GitPushReviewer", function()
  local output = vim.fn.system("gitlab-reviewer")

  if vim.v.shell_error ~= 0 then
    vim.notify("gitlab-reviewer failed: " .. output, vim.log.levels.ERROR)
    return
  end

  local members = {}
  for line in output:gmatch("[^\r\n]+") do
    local name, username = line:match("^(.-)\t(.*)$")
    if name then
      table.insert(members, { name = name, username = username })
    end
  end

  if #members == 0 then
    vim.notify("No reviewers found", vim.log.levels.WARN)
    return
  end

  local display = {}
  for _, m in ipairs(members) do
    if m.username ~= "" then
      table.insert(display, m.name .. " (@" .. m.username .. ")")
    else
      table.insert(display, m.name .. " (no username)")
    end
  end

  vim.ui.select(display, { prompt = "Select reviewer:" }, function(choice, idx)
    if not choice or not idx then
      return
    end

    local selected = members[idx]

    if selected.username == "" then
      vim.ui.input({ prompt = "GitLab username for " .. selected.name .. ": " }, function(input)
        if input and input ~= "" then
          vim.cmd("Git mpr " .. input)
        end
      end)
    else
      vim.cmd("Git mpr " .. selected.username)
    end
  end)
end, { desc = "Push and create MR with reviewer" })
```

This requires [vim-fugitive](https://github.com/tpope/vim-fugitive) for the
`Git mpr` command and the `mpr` git alias from above.
