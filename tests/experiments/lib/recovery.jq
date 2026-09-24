# The catch-up phase of one cell in four numbers: what the tool needed to
# finish the release its first run left partial, once the fault was gone.
#
#   jq -s --argjson planEmpty <true|false> --slurpfile last <observation> \
#     -f recovery.jq steps.jsonl
#
# The input is every step the protocol recorded, as common.sh writes them:
# {step, exit, kind, phase, seconds}. `last` is the last observation the run
# took, and `planEmpty` is the tool's own answer, read by common.sh's
# plan_is_empty from the last question the catch-up asked it, that it would
# release nothing next. The output is null when the protocol ran no catch-up
# phase, so a cell that measures none carries no summary at all.
#
#   runs             uninterrupted passes of the tool's own commands. A pass
#                    starts at a release command and ends at a manual step, or
#                    at a release command that failed; a question asked of
#                    the tool in between neither counts nor interrupts.
#   releaseCommands  the tool's own release commands the catch-up ran
#   manualCommands   the steps an operator did by hand
#   converged        the last observation has every package consistent or at
#                    its baseline, the clone clean and level with its origin,
#                    and the tool's next plan is empty

def counted: map(select(.phase == "catch-up" and .kind != "query"));

def passes:
  reduce .[] as $step ({passes: 0, open: false};
    if $step.kind == "release" then
      (if .open then . else .passes += 1 | .open = true end)
      | (if $step.exit != 0 then .open = false else . end)
    else
      .open = false
    end)
  | .passes;

def settled:
  (.packages | type == "object")
  and (.packages | to_entries | all(.value.state == "consistent" or .value.state == "baseline"))
  and .local.dirty == false
  and .local.mergeInProgress == false
  and .local.rebaseInProgress == false
  and .local.aheadOfOrigin == 0
  and .local.behindOrigin == 0;

if any(.[]; .phase == "catch-up") | not then
  null
else
  counted as $steps
  | {runs: ($steps | passes),
     releaseCommands: ($steps | map(select(.kind == "release")) | length),
     manualCommands: ($steps | map(select(.kind == "manual")) | length),
     converged: (($last | length) > 0 and ($last[0] | settled) and $planEmpty)}
end
