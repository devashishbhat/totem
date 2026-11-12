//! Criterion bench for the matchers. The target is 100k
//! characters against 50 candidates in under 2 ms.

use criterion::{criterion_group, criterion_main, Criterion};
use totem_core::{match_in_context, match_value_flow};

fn corpus() -> (Vec<(String, String)>, Vec<String>) {
    let candidates: Vec<(String, String)> = (0..50)
        .map(|i| {
            (
                format!("mem{i}"),
                format!(
                    "Invoices for vendor {i} go to billing{i}@acme-payments.example and are \
                     approved by the finance team on the first Friday of the month."
                ),
            )
        })
        .collect();
    // The target is 100k characters against 50 candidates in under 2 ms,
    // so the prompt is built to that size with one candidate planted in it.
    let unit = "The quarterly review covers spend, approvals and vendor onboarding. ";
    let planted = format!("\nRemembered: {}\n", candidates[7].1);
    let repeats = (100_000 - planted.chars().count()) / unit.chars().count();
    let filler = unit.repeat(repeats / 2);
    let message = format!("{filler}{planted}{filler}");
    (candidates, vec![message])
}

fn bench_in_context(c: &mut Criterion) {
    let (candidates, messages) = corpus();
    let chars: usize = messages.iter().map(|m| m.chars().count()).sum();
    c.bench_function(
        &format!("match_in_context/{chars}chars/50candidates"),
        |b| b.iter(|| match_in_context(&candidates, &messages)),
    );
}

fn bench_value_flow(c: &mut Criterion) {
    let (candidates, _) = corpus();
    let args = r#"{"to": "billing7@acme-payments.example", "body": "October invoice attached.", "cc": ["ap@acme.example"]}"#;
    c.bench_function("match_value_flow/50candidates", |b| {
        b.iter(|| match_value_flow(args, &candidates))
    });
}

criterion_group!(benches, bench_in_context, bench_value_flow);
criterion_main!(benches);
