export function getPreviousUserMessageTop(scrollArea: HTMLDivElement) {
  const scrollAreaTop = scrollArea.getBoundingClientRect().top;
  const userMessages = Array.from(
    scrollArea.querySelectorAll<HTMLElement>("[data-user-message]"),
  );

  return userMessages
    .map(
      (message) =>
        message.getBoundingClientRect().top -
        scrollAreaTop +
        scrollArea.scrollTop,
    )
    .findLast((position) => position < scrollArea.scrollTop - 8);
}

export function getNextUserMessageTop(scrollArea: HTMLDivElement) {
  const scrollAreaTop = scrollArea.getBoundingClientRect().top;
  const positions = Array.from(
    scrollArea.querySelectorAll<HTMLElement>("[data-user-message]"),
  ).map(
    (message) =>
      message.getBoundingClientRect().top -
      scrollAreaTop +
      scrollArea.scrollTop,
  );
  return positions.find((position) => position > scrollArea.scrollTop + 24);
}
