import { expect, test, type Page } from "@playwright/test";
import {
  buildInReviewSubmissionSQL,
  buildWantedStateSQL,
  captureRequests,
  fetchLeaderboard,
  fetchScoreboard,
  fetchTestState,
  findCapturedRequest,
  findHostedRequest,
  findLeaderboardEntry,
  findPullRequest,
  findRepository,
  findRow,
  findScoreboardEntry,
  mergePullRequest,
  newActorPage,
  newApiRequestContext,
  resetTestState,
  seedDoltHub,
  seedWantedAcrossRepos,
} from "./helpers";

test.setTimeout(240_000);

test("runs the real hosted PR-overlay lifecycle across three users and merges the accepted branch upstream", async ({
  browser,
}) => {
  const api = await newApiRequestContext();
  const wantedID = "w-pr-overlay";
  const title = "Exercise PR overlay";
  const description =
    "Competing upstream submissions should be actionable by the poster.";
  const bobEvidence = "https://example.test/bob";
  const charlieEvidence = "https://example.test/charlie";
  const acceptMessage = "Selecting Bob after comparing both submissions";

  await resetTestState(api);
  await seedWantedAcrossRepos(api, wantedID, title, description);

  const alice = await newActorPage(browser, "alice");
  const bob = await newActorPage(browser, "bob");
  const charlie = await newActorPage(browser, "charlie");

  const aliceRequests = captureRequests(alice.page);
  const bobRequests = captureRequests(bob.page);
  const charlieRequests = captureRequests(charlie.page);

  try {
    await bob.page.goto(`/wanted/${wantedID}`);
    await expect(bob.page.getByRole("heading", { name: title })).toBeVisible();

    const bobClaimResponse = bob.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === `/api/wanted/${wantedID}/claim`
      );
    });
    await bob.page.locator('button[data-action="claim"]').click();
    await expect((await bobClaimResponse).ok()).toBeTruthy();
    await expect(bob.page.locator('button[data-action="done"]')).toBeVisible();

    await bob.page.locator('button[data-action="done"]').click();
    await bob.page.getByPlaceholder("https://github.com/...").fill(bobEvidence);
    const bobDoneResponse = bob.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === `/api/wanted/${wantedID}/done`
      );
    });
    await bob.page.getByRole("button", { name: "Submit", exact: true }).click();
    await expect((await bobDoneResponse).ok()).toBeTruthy();

    await charlie.page.goto(`/wanted/${wantedID}`);
    await expect(
      charlie.page.getByRole("heading", { name: title }),
    ).toBeVisible();

    const charlieClaimResponse = charlie.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === `/api/wanted/${wantedID}/claim`
      );
    });
    await charlie.page.locator('button[data-action="claim"]').click();
    await expect((await charlieClaimResponse).ok()).toBeTruthy();
    await expect(
      charlie.page.locator('button[data-action="done"]'),
    ).toBeVisible();

    await charlie.page.locator('button[data-action="done"]').click();
    await charlie.page
      .getByPlaceholder("https://github.com/...")
      .fill(charlieEvidence);
    const charlieDoneResponse = charlie.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === `/api/wanted/${wantedID}/done`
      );
    });
    await charlie.page
      .getByRole("button", { name: "Submit", exact: true })
      .click();
    await expect((await charlieDoneResponse).ok()).toBeTruthy();

    await alice.page.goto(`/wanted/${wantedID}`);
    await expect(
      alice.page.getByRole("heading", { name: title }),
    ).toBeVisible();
    await expect(
      alice.page.getByText(bobEvidence, { exact: true }).first(),
    ).toBeVisible();
    await expect(
      alice.page.getByText(charlieEvidence, { exact: true }).first(),
    ).toBeVisible();

    const bobSubmission = alice.page
      .getByText(bobEvidence, { exact: true })
      .locator('xpath=ancestor::div[.//button[@data-action="accept"]][1]')
      .first();
    await expect(
      bobSubmission.locator('button[data-action="accept"]'),
    ).toBeVisible();

    const acceptResponse = alice.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === `/api/wanted/${wantedID}/accept-upstream`
      );
    });
    await bobSubmission.locator('button[data-action="accept"]').click();

    const acceptDialog = alice.page.getByRole("dialog", {
      name: "Accept Submission",
    });
    await acceptDialog.locator("select").nth(0).selectOption("4");
    await acceptDialog.locator("select").nth(1).selectOption("3");
    await acceptDialog.locator("select").nth(2).selectOption("branch");
    await acceptDialog.locator("textarea").fill(acceptMessage);
    await acceptDialog.getByRole("button", { name: "Accept" }).click();
    await expect((await acceptResponse).ok()).toBeTruthy();

    await expect(alice.page.getByText(/^Completed by:\s*bob$/)).toBeVisible();
    await expect(alice.page.getByText(/^Validated by:\s*alice$/)).toBeVisible();
    await expect(alice.page.getByText(acceptMessage)).toBeVisible();

    const acceptedState = await fetchTestState(api);
    const bobPR = findPullRequest(acceptedState, {
      upstreamOwner: "e2e",
      upstreamDB: "wl-commons",
      fromOwner: "bob",
      fromBranch: `wl/bob/${wantedID}`,
    });
    const charliePR = findPullRequest(acceptedState, {
      upstreamOwner: "e2e",
      upstreamDB: "wl-commons",
      fromOwner: "charlie",
      fromBranch: `wl/charlie/${wantedID}`,
    });
    const alicePR = findPullRequest(acceptedState, {
      upstreamOwner: "e2e",
      upstreamDB: "wl-commons",
      fromOwner: "alice",
      fromBranch: `wl/alice/${wantedID}`,
    });
    expect(alicePR.state).toBe("open");

    await mergePullRequest(api, alicePR.id);

    await expect
      .poll(async () => {
        const leaderboard = await fetchLeaderboard(api);
        return findLeaderboardEntry(leaderboard, "bob").completions;
      })
      .toBe(1);

    const leaderboard = await fetchLeaderboard(api);
    const leaderboardBob = findLeaderboardEntry(leaderboard, "bob");
    expect(leaderboardBob.avg_quality).toBeCloseTo(4, 5);
    expect(leaderboardBob.avg_reliability).toBeCloseTo(3, 5);

    await expect
      .poll(async () => {
        const scoreboard = await fetchScoreboard(api);
        return findScoreboardEntry(scoreboard, "bob").weighted_score;
      })
      .toBe(3);

    const scoreboard = await fetchScoreboard(api);
    const scoreboardBob = findScoreboardEntry(scoreboard, "bob");
    expect(scoreboardBob).toMatchObject({
      rig_handle: "bob",
      display_name: "Bob",
      trust_tier: "newcomer",
      stamp_count: 1,
      weighted_score: 3,
      unique_towns: 1,
      completions: 1,
    });
    expect(scoreboardBob.avg_quality).toBeCloseTo(4, 5);
    expect(scoreboardBob.avg_reliability).toBeCloseTo(3, 5);

    await alice.page.goto("/scoreboard");
    await expect(
      alice.page.getByRole("heading", { name: "Scoreboard" }),
    ).toBeVisible();
    await expect(
      alice.page.getByRole("link", { name: "bob" }).first(),
    ).toBeVisible();
    await expect(alice.page.getByText("newcomer").first()).toBeVisible();

    const finalState = await fetchTestState(api);
    const upstreamRepo = findRepository(finalState, "e2e", "wl-commons");
    const upstreamMain = upstreamRepo.branches["main"];
    expect(findRow(upstreamMain.wanted, "id", wantedID)).toMatchObject({
      id: wantedID,
      status: "completed",
      claimed_by: "bob",
      posted_by: "alice",
    });
    expect(
      findRow(upstreamMain.completions, "wanted_id", wantedID),
    ).toMatchObject({
      wanted_id: wantedID,
      completed_by: "bob",
      validated_by: "alice",
      evidence: bobEvidence,
    });
    expect(findRow(upstreamMain.stamps, "subject", "bob")).toMatchObject({
      author: "alice",
      subject: "bob",
      severity: "branch",
      message: acceptMessage,
    });

    expect(
      findPullRequest(finalState, {
        upstreamOwner: "e2e",
        upstreamDB: "wl-commons",
        fromOwner: "alice",
        fromBranch: `wl/alice/${wantedID}`,
      }).state,
    ).toBe("closed");
    expect(
      findPullRequest(finalState, {
        upstreamOwner: "e2e",
        upstreamDB: "wl-commons",
        fromOwner: "bob",
        fromBranch: `wl/bob/${wantedID}`,
      }).url,
    ).toBe(bobPR.url);
    expect(
      findPullRequest(finalState, {
        upstreamOwner: "e2e",
        upstreamDB: "wl-commons",
        fromOwner: "charlie",
        fromBranch: `wl/charlie/${wantedID}`,
      }).url,
    ).toBe(charliePR.url);

    const bobClaimRequest = findCapturedRequest(
      bobRequests,
      `/api/wanted/${wantedID}/claim`,
      "POST",
    );
    expect(bobClaimRequest.headers["x-wasteland"]).toBe("e2e/wl-commons");

    const bobDoneRequest = findCapturedRequest(
      bobRequests,
      `/api/wanted/${wantedID}/done`,
      "POST",
    );
    expect(JSON.parse(bobDoneRequest.body || "{}")).toMatchObject({
      evidence: bobEvidence,
    });

    const charlieDoneRequest = findCapturedRequest(
      charlieRequests,
      `/api/wanted/${wantedID}/done`,
      "POST",
    );
    expect(JSON.parse(charlieDoneRequest.body || "{}")).toMatchObject({
      evidence: charlieEvidence,
    });

    const acceptRequest = findCapturedRequest(
      aliceRequests,
      `/api/wanted/${wantedID}/accept-upstream`,
      "POST",
    );
    expect(JSON.parse(acceptRequest.body || "{}")).toMatchObject({
      rig_handle: "bob",
      pr_url: `https://www.dolthub.com/repositories/e2e/wl-commons/pulls/${bobPR.id}`,
      quality: 4,
      reliability: 3,
      severity: "branch",
      message: acceptMessage,
    });

    findHostedRequest(
      finalState.hosted_requests,
      `/api/wanted/${wantedID}/claim`,
      "POST",
    );
    findHostedRequest(
      finalState.hosted_requests,
      `/api/wanted/${wantedID}/done`,
      "POST",
    );
    findHostedRequest(
      finalState.hosted_requests,
      `/api/wanted/${wantedID}/accept-upstream`,
      "POST",
    );
  } finally {
    await Promise.all([
      alice.context.close(),
      bob.context.close(),
      charlie.context.close(),
    ]);
    await api.dispose().catch(() => {});
  }
});

test("keeps combined mainline and upstream submissions distinct when the poster selects one upstream PR", async ({
  browser,
}) => {
  const api = await newApiRequestContext();
  const wantedID = "w-pr-overlay-combined";
  const title = "Combined mainline and upstream review";
  const description =
    "Mainline review plus multiple upstream PRs should remain separately actionable.";
  const mainEvidence = "https://example.test/alice-mainline";
  const bobCompletedEvidence = "https://example.test/bob-completed";
  const bobReviewEvidence = "https://example.test/bob-review";
  const acceptMessage = "Selecting Bob's in-review fork over the mainline draft";

  await resetTestState(api);
  await seedWantedAcrossRepos(api, wantedID, title, description);
  await seedDoltHub(api, {
    repositories: [
      {
        owner: "e2e",
        db: "wl-commons",
        main_sql: buildWantedStateSQL({
          wantedID,
          status: "in_review",
          claimedBy: "alice",
          completedBy: "alice",
          evidence: mainEvidence,
          completionID: `c-${wantedID}-alice-main`,
        }),
      },
      {
        owner: "bob",
        db: "wl-commons",
        main_sql: buildWantedStateSQL({
          wantedID,
          status: "completed",
          claimedBy: "bob",
          completedBy: "bob",
          evidence: bobCompletedEvidence,
          completionID: `c-${wantedID}-bob-main`,
        }),
        branches: [
          {
            name: `wl/bob/${wantedID}`,
            sql: buildWantedStateSQL({
              wantedID,
              status: "in_review",
              claimedBy: "bob",
              completedBy: "bob",
              evidence: bobReviewEvidence,
              completionID: `c-${wantedID}-bob-review`,
            }),
          },
        ],
      },
    ],
    prs: [
      {
        id: "101",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "bob",
        from_branch: "main",
        author: "bob",
        title: "Bob completed submission",
      },
      {
        id: "102",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "bob",
        from_branch: `wl/bob/${wantedID}`,
        author: "bob",
        title: "Bob in-review submission",
      },
    ],
  });

  const alice = await newActorPage(browser, "alice");
  const aliceRequests = captureRequests(alice.page);

  try {
    await alice.page.goto(`/wanted/${wantedID}`);
    await expect(
      alice.page.getByRole("heading", { name: title }),
    ).toBeVisible();
    await expect(alice.page.getByText(mainEvidence)).toBeVisible();
    await expect(alice.page.getByText(bobCompletedEvidence)).toBeVisible();
    await expect(alice.page.getByText(bobReviewEvidence)).toBeVisible();
    await expect(alice.page.locator('button[data-action="accept"]')).toHaveCount(
      2,
    );

    const mainSubmission = submissionContainerByText(alice.page, mainEvidence);
    await expect(mainSubmission).toContainText("(main)");

    const completedSubmission = submissionContainerByText(
      alice.page,
      bobCompletedEvidence,
    );
    await expect(
      completedSubmission.locator('button[data-action="accept"]'),
    ).toHaveCount(0);
    await expect(
      completedSubmission.locator('button[data-action="close"]'),
    ).toHaveCount(0);
    await expect(
      completedSubmission.locator('button[data-action="reject"]'),
    ).toBeVisible();

    const reviewSubmission = submissionContainerByText(
      alice.page,
      bobReviewEvidence,
    );
    const acceptResponse = alice.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === `/api/wanted/${wantedID}/accept-upstream`
      );
    });
    await reviewSubmission.locator('button[data-action="accept"]').click();

    const acceptDialog = alice.page.getByRole("dialog", {
      name: "Accept Submission",
    });
    await acceptDialog.locator("select").nth(0).selectOption("5");
    await acceptDialog.locator("select").nth(1).selectOption("4");
    await acceptDialog.locator("select").nth(2).selectOption("branch");
    await acceptDialog.locator("textarea").fill(acceptMessage);
    await acceptDialog.getByRole("button", { name: "Accept" }).click();
    await expect((await acceptResponse).ok()).toBeTruthy();

    const acceptRequest = findCapturedRequest(
      aliceRequests,
      `/api/wanted/${wantedID}/accept-upstream`,
      "POST",
    );
    expect(JSON.parse(acceptRequest.body || "{}")).toMatchObject({
      rig_handle: "bob",
      pr_url: "https://www.dolthub.com/repositories/e2e/wl-commons/pulls/102",
      quality: 5,
      reliability: 4,
      severity: "branch",
      message: acceptMessage,
    });

    const acceptedState = await fetchTestState(api);
    const alicePR = findPullRequest(acceptedState, {
      upstreamOwner: "e2e",
      upstreamDB: "wl-commons",
      fromOwner: "alice",
      fromBranch: `wl/alice/${wantedID}`,
    });
    await mergePullRequest(api, alicePR.id);

    const finalState = await fetchTestState(api);
    const upstreamRepo = findRepository(finalState, "e2e", "wl-commons");
    const upstreamMain = upstreamRepo.branches["main"];
    expect(
      findRow(upstreamMain.completions, "wanted_id", wantedID),
    ).toMatchObject({
      wanted_id: wantedID,
      completed_by: "bob",
      evidence: bobReviewEvidence,
      validated_by: "alice",
    });
    expect(findRow(upstreamMain.stamps, "subject", "bob")).toMatchObject({
      author: "alice",
      message: acceptMessage,
    });
  } finally {
    await Promise.all([alice.context.close()]);
    await api.dispose().catch(() => {});
  }
});

test("uses pr_url to disambiguate multiple upstream PR submissions from the same rig", async ({
  browser,
}) => {
  const api = await newApiRequestContext();
  const wantedID = "w-pr-overlay-duplicate";
  const title = "Disambiguate duplicate submissions";
  const description =
    "Poster actions should target the selected PR when one rig has multiple open submissions.";
  const firstBranch = `wl/bob/${wantedID}-one`;
  const secondBranch = `wl/bob/${wantedID}-two`;
  const firstEvidence = "https://example.test/bob-first";
  const secondEvidence = "https://example.test/bob-second";
  const acceptMessage = "Selected the second Bob submission";

  await resetTestState(api);
  await seedWantedAcrossRepos(api, wantedID, title, description);
  await seedDoltHub(api, {
    repositories: [
      {
        owner: "bob",
        db: "wl-commons",
        branches: [
          {
            name: firstBranch,
            sql: buildInReviewSubmissionSQL(
              wantedID,
              "bob",
              firstEvidence,
              `c-${wantedID}-bob-1`,
            ),
          },
          {
            name: secondBranch,
            sql: buildInReviewSubmissionSQL(
              wantedID,
              "bob",
              secondEvidence,
              `c-${wantedID}-bob-2`,
            ),
          },
        ],
      },
    ],
    prs: [
      {
        id: "41",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "bob",
        from_branch: firstBranch,
        author: "bob",
        title: "First Bob submission",
      },
      {
        id: "42",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "bob",
        from_branch: secondBranch,
        author: "bob",
        title: "Second Bob submission",
      },
    ],
  });

  const seededState = await fetchTestState(api);
  const firstPR = findPullRequest(seededState, {
    upstreamOwner: "e2e",
    upstreamDB: "wl-commons",
    fromOwner: "bob",
    fromBranch: firstBranch,
  });
  const secondPR = findPullRequest(seededState, {
    upstreamOwner: "e2e",
    upstreamDB: "wl-commons",
    fromOwner: "bob",
    fromBranch: secondBranch,
  });

  const alice = await newActorPage(browser, "alice");
  const aliceRequests = captureRequests(alice.page);

  try {
    await alice.page.goto(`/wanted/${wantedID}`);
    await expect(
      alice.page.getByRole("heading", { name: title }),
    ).toBeVisible();
    await expect(
      alice.page.locator('button[data-action="accept"]'),
    ).toHaveCount(2);
    await expect(alice.page.getByText(secondEvidence)).toBeVisible();

    const secondSubmission = alice.page
      .getByText(secondEvidence, { exact: true })
      .locator('xpath=ancestor::div[.//button[@data-action="accept"]][1]')
      .first();
    await expect(
      secondSubmission.locator('button[data-action="accept"]'),
    ).toBeVisible();

    const acceptResponse = alice.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === `/api/wanted/${wantedID}/accept-upstream`
      );
    });
    await secondSubmission.locator('button[data-action="accept"]').click();

    const acceptDialog = alice.page.getByRole("dialog", {
      name: "Accept Submission",
    });
    await acceptDialog.locator("select").nth(0).selectOption("5");
    await acceptDialog.locator("select").nth(1).selectOption("4");
    await acceptDialog.locator("select").nth(2).selectOption("branch");
    await acceptDialog.locator("textarea").fill(acceptMessage);
    await acceptDialog.getByRole("button", { name: "Accept" }).click();
    await expect((await acceptResponse).ok()).toBeTruthy();

    const acceptRequest = findCapturedRequest(
      aliceRequests,
      `/api/wanted/${wantedID}/accept-upstream`,
      "POST",
    );
    expect(JSON.parse(acceptRequest.body || "{}")).toMatchObject({
      rig_handle: "bob",
      pr_url: `https://www.dolthub.com/repositories/e2e/wl-commons/pulls/${secondPR.id}`,
      quality: 5,
      reliability: 4,
      severity: "branch",
      message: acceptMessage,
    });

    const acceptedState = await fetchTestState(api);
    const alicePR = findPullRequest(acceptedState, {
      upstreamOwner: "e2e",
      upstreamDB: "wl-commons",
      fromOwner: "alice",
      fromBranch: `wl/alice/${wantedID}`,
    });
    await mergePullRequest(api, alicePR.id);

    const finalState = await fetchTestState(api);
    const upstreamRepo = findRepository(finalState, "e2e", "wl-commons");
    const upstreamMain = upstreamRepo.branches["main"];
    expect(
      findRow(upstreamMain.completions, "wanted_id", wantedID),
    ).toMatchObject({
      wanted_id: wantedID,
      completed_by: "bob",
      evidence: secondEvidence,
      validated_by: "alice",
    });
    expect(findRow(upstreamMain.stamps, "subject", "bob")).toMatchObject({
      author: "alice",
      message: acceptMessage,
    });
    expect(firstPR.url).not.toBe(secondPR.url);
  } finally {
    await Promise.all([alice.context.close()]);
    await api.dispose().catch(() => {});
  }
});

test("handles mixed submission states and case-variant rigs without losing the selected upstream PR", async ({
  browser,
}) => {
  const api = await newApiRequestContext();
  const wantedID = "w-pr-overlay-mixed";
  const title = "Mixed competing submissions";
  const description =
    "Claimed rows, duplicate PRs, and case-variant rigs should stay distinct.";
  const antdittoMainEvidence = "https://example.test/antditto-main";
  const antdittoBranchEvidence = "https://example.test/antditto-branch";
  const upperRigEvidence = "https://example.test/dpr-upper";
  const lowerRigEvidence = "https://example.test/dpr-lower";
  const acceptMessage = "Selected antditto's branch submission";

  await resetTestState(api);
  await seedWantedAcrossRepos(api, wantedID, title, description);
  await seedDoltHub(api, {
    repositories: [
      {
        owner: "DreadPirateRobertz",
        db: "wl-commons",
        fork_of: { owner: "e2e", db: "wl-commons" },
        main_sql: buildWantedStateSQL({
          wantedID,
          status: "in_review",
          claimedBy: "DreadPirateRobertz",
          completedBy: "DreadPirateRobertz",
          evidence: upperRigEvidence,
          completionID: `c-${wantedID}-upper`,
        }),
      },
      {
        owner: "dreadpiraterobertz",
        db: "wl-commons",
        fork_of: { owner: "e2e", db: "wl-commons" },
        main_sql: buildWantedStateSQL({
          wantedID,
          status: "in_review",
          claimedBy: "dreadpiraterobertz",
          completedBy: "dreadpiraterobertz",
          evidence: lowerRigEvidence,
          completionID: `c-${wantedID}-lower`,
        }),
      },
      {
        owner: "highb",
        db: "wl-commons",
        fork_of: { owner: "e2e", db: "wl-commons" },
        main_sql: buildWantedStateSQL({
          wantedID,
          status: "claimed",
          claimedBy: "highb",
        }),
      },
      {
        owner: "antditto",
        db: "wl-commons",
        fork_of: { owner: "e2e", db: "wl-commons" },
        main_sql: buildWantedStateSQL({
          wantedID,
          status: "in_review",
          completedBy: "antditto",
          evidence: antdittoMainEvidence,
          completionID: `c-${wantedID}-antditto-main`,
        }),
        branches: [
          {
            name: `wl/antditto/${wantedID}`,
            sql: buildWantedStateSQL({
              wantedID,
              status: "in_review",
              claimedBy: "antditto",
              completedBy: "antditto",
              evidence: antdittoBranchEvidence,
              completionID: `c-${wantedID}-antditto-branch`,
            }),
          },
        ],
      },
    ],
    prs: [
      {
        id: "201",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "DreadPirateRobertz",
        from_branch: "main",
        author: "DreadPirateRobertz",
        title: "Uppercase competitor",
      },
      {
        id: "202",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "dreadpiraterobertz",
        from_branch: "main",
        author: "dreadpiraterobertz",
        title: "Lowercase competitor",
      },
      {
        id: "203",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "highb",
        from_branch: "main",
        author: "highb",
        title: "Claimed competitor",
      },
      {
        id: "204",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "antditto",
        from_branch: "main",
        author: "antditto",
        title: "Antditto main competitor",
      },
      {
        id: "205",
        upstream_owner: "e2e",
        upstream_db: "wl-commons",
        from_owner: "antditto",
        from_branch: `wl/antditto/${wantedID}`,
        author: "antditto",
        title: "Antditto branch competitor",
      },
    ],
  });

  const alice = await newActorPage(browser, "alice");
  const aliceRequests = captureRequests(alice.page);

  try {
    await alice.page.goto(`/wanted/${wantedID}`);
    await expect(
      alice.page.getByRole("heading", { name: title }),
    ).toBeVisible();
    await expect(
      alice.page.getByText("DreadPirateRobertz", { exact: true }),
    ).toBeVisible();
    await expect(
      alice.page.getByText("dreadpiraterobertz", { exact: true }),
    ).toBeVisible();
    await expect(alice.page.getByText(antdittoMainEvidence)).toBeVisible();
    await expect(alice.page.getByText(antdittoBranchEvidence)).toBeVisible();
    await expect(alice.page.locator('button[data-action="accept"]')).toHaveCount(
      4,
    );

    const highbSubmission = submissionContainerByText(alice.page, "highb");
    await expect(
      highbSubmission.locator('button[data-action="accept"]'),
    ).toHaveCount(0);
    await expect(
      highbSubmission.locator('button[data-action="close"]'),
    ).toHaveCount(0);
    await expect(
      highbSubmission.locator('button[data-action="reject"]'),
    ).toBeVisible();

    const antdittoBranchSubmission = submissionContainerByText(
      alice.page,
      antdittoBranchEvidence,
    );
    const acceptResponse = alice.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "POST" &&
        url.pathname === `/api/wanted/${wantedID}/accept-upstream`
      );
    });
    await antdittoBranchSubmission
      .locator('button[data-action="accept"]')
      .click();

    const acceptDialog = alice.page.getByRole("dialog", {
      name: "Accept Submission",
    });
    await acceptDialog.locator("select").nth(0).selectOption("4");
    await acceptDialog.locator("select").nth(1).selectOption("4");
    await acceptDialog.locator("select").nth(2).selectOption("branch");
    await acceptDialog.locator("textarea").fill(acceptMessage);
    await acceptDialog.getByRole("button", { name: "Accept" }).click();
    await expect((await acceptResponse).ok()).toBeTruthy();

    const acceptRequest = findCapturedRequest(
      aliceRequests,
      `/api/wanted/${wantedID}/accept-upstream`,
      "POST",
    );
    expect(JSON.parse(acceptRequest.body || "{}")).toMatchObject({
      rig_handle: "antditto",
      pr_url: "https://www.dolthub.com/repositories/e2e/wl-commons/pulls/205",
      quality: 4,
      reliability: 4,
      severity: "branch",
      message: acceptMessage,
    });

    const acceptedState = await fetchTestState(api);
    const alicePR = findPullRequest(acceptedState, {
      upstreamOwner: "e2e",
      upstreamDB: "wl-commons",
      fromOwner: "alice",
      fromBranch: `wl/alice/${wantedID}`,
    });
    await mergePullRequest(api, alicePR.id);

    const finalState = await fetchTestState(api);
    const upstreamRepo = findRepository(finalState, "e2e", "wl-commons");
    const upstreamMain = upstreamRepo.branches["main"];
    expect(
      findRow(upstreamMain.completions, "wanted_id", wantedID),
    ).toMatchObject({
      wanted_id: wantedID,
      completed_by: "antditto",
      evidence: antdittoBranchEvidence,
      validated_by: "alice",
    });
  } finally {
    await Promise.all([alice.context.close()]);
    await api.dispose().catch(() => {});
  }
});

test("surfaces validated items in browse filters and detail views", async ({
  browser,
}) => {
  const api = await newApiRequestContext();
  const validatedID = "w-validated-prod-shape";
  const openID = "w-open-prod-shape";
  const validatedTitle = "Validated production-shaped task";
  const openTitle = "Open task for filter contrast";

  await resetTestState(api);
  await seedWantedAcrossRepos(
    api,
    validatedID,
    validatedTitle,
    "Validated items should remain discoverable in browse filters.",
  );
  await seedWantedAcrossRepos(
    api,
    openID,
    openTitle,
    "This item should disappear when filtering to validated.",
  );
  await seedDoltHub(api, {
    repositories: [
      {
        owner: "e2e",
        db: "wl-commons",
        main_sql: buildWantedStateSQL({
          wantedID: validatedID,
          status: "validated",
          claimedBy: "gastown-ci",
        }),
      },
    ],
  });

  const alice = await newActorPage(browser, "alice");
  const aliceRequests = captureRequests(alice.page);

  try {
    await alice.page.goto("/");
    await expect(
      alice.page.getByRole("heading", { name: "Wanted Board" }),
    ).toBeVisible();
    await expect(
      alice.page.getByRole("link", { name: validatedTitle }),
    ).toBeVisible();
    await expect(alice.page.getByRole("link", { name: openTitle })).toBeVisible();

    const filterResponse = alice.page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        response.request().method() === "GET" &&
        url.pathname === "/api/wanted" &&
        url.searchParams.get("status") === "validated"
      );
    });
    await alice.page.getByLabel("Filter by status").selectOption("validated");
    await expect((await filterResponse).ok()).toBeTruthy();

    await expect(
      alice.page.getByRole("link", { name: validatedTitle }),
    ).toBeVisible();
    await expect(
      alice.page.getByRole("link", { name: openTitle }),
    ).toHaveCount(0);
    expect(
      aliceRequests.some((request) => {
        const url = new URL(request.url);
        return (
          request.method === "GET" &&
          request.path === "/api/wanted" &&
          url.searchParams.get("status") === "validated"
        );
      }),
    ).toBeTruthy();

    await alice.page.getByRole("link", { name: validatedTitle }).click();
    await expect(
      alice.page.locator('[data-status="validated"]').first(),
    ).toBeVisible();
  } finally {
    await Promise.all([alice.context.close()]);
    await api.dispose().catch(() => {});
  }
});

function submissionContainerByText(page: Page, text: string) {
  return page
    .getByText(text, { exact: true })
    .locator('xpath=ancestor::div[.//button[@data-action="reject"]][1]')
    .first();
}
